package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type plantNode struct {
	cfg         nodeConfig
	m           manifest
	dir         string
	token       string
	mu          sync.Mutex
	d           delivery
	lastReceive time.Time
	pending     []time.Time
	queues      [2]*subscriberQueue
	control     atomic.Pointer[faultControl]
	ledger      *os.File
}

type subscriberQueue struct {
	mu          sync.Mutex
	items       []queuedFrame
	maximum     int
	drops, sent uint64
	unacked     int
}

func (q *subscriberQueue) push(b []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 1000 {
		q.drops++
		return
	}
	q.items = append(q.items, queuedFrame{b, time.Now()})
	if len(q.items) > q.maximum {
		q.maximum = len(q.items)
	}
}

func (q *subscriberQueue) pop() []byte {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	b := q.items[0].Data
	q.items[0] = queuedFrame{}
	q.items = q.items[1:]
	return b
}

func (q *subscriberQueue) snapshot(id string) queueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	age := 0.0
	if len(q.items) > 0 {
		age = time.Since(q.items[0].Arrived).Seconds()
	}
	s := queueStats{id, len(q.items), q.maximum, age, q.unacked, q.drops, q.sent}
	q.maximum = len(q.items)
	return s
}

func runNode(dir, id string) error {
	var m manifest
	if err := readJSON(filepath.Join(dir, "manifest.json"), &m); err != nil {
		return err
	}
	var cfg nodeConfig
	for _, n := range m.Nodes {
		if n.ID == id {
			cfg = n
		}
	}
	if cfg.ID == "" {
		return fmt.Errorf("unknown process")
	}
	token, err := os.ReadFile(filepath.Join(dir, "collector.token"))
	if err != nil {
		return err
	}
	ledger, err := os.OpenFile(filepath.Join(dir, id+".ledger.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer ledger.Close()
	p := &plantNode{cfg: cfg, m: m, dir: dir, token: string(token), ledger: ledger, lastReceive: time.Now()}
	p.d.AgeBuckets = make([]uint64, len(ageBuckets))
	p.control.Store(&faultControl{})
	for i := range p.queues {
		p.queues[i] = &subscriberQueue{}
	}
	go p.telemetry()
	switch cfg.Role {
	case "publisher":
		return p.publish()
	case "relay":
		for i := 0; i < 2; i++ {
			go p.dispatch(i)
		}
		return p.relay()
	case "subscriber":
		return p.subscribe()
	}
	return fmt.Errorf("invalid role")
}

func address(port int) string { return fmt.Sprintf("127.0.0.1:%d", port) }

func (p *plantNode) ledgerEvent(event string, seq uint64) {
	// Independent sequence receipts, never derived from collector or diagnostic answers.
	_, err := fmt.Fprintf(p.ledger, "{\"at\":%q,\"event\":%q,\"sequence\":%d}\n", time.Now().UTC().Format(time.RFC3339Nano), event, seq)
	if err != nil {
		log.Fatal(err)
	}
}

func (p *plantNode) publish() error {
	var seq uint64
	for {
		c, err := net.DialTimeout("tcp", address(p.cfg.Base), time.Second)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		p.ledgerEvent("connected", seq)
		p.mu.Lock()
		p.d.Reconnects++
		p.mu.Unlock()
		tick := time.NewTicker(10 * time.Millisecond)
		credit := 0.0
		connected := true
		for connected {
			<-tick.C
			ctl := p.control.Load()
			if ctl.Pause {
				credit = 0
				continue
			}
			rate := ctl.Rate
			if rate == 0 {
				rate = 500
			}
			credit += float64(rate) / 100
			for credit >= 1 {
				credit--
				seq++
				b := encodeFrame(frame{p.m.Run, p.cfg.Chain[0], p.cfg.WireGeneration, seq, time.Now().UnixNano()})
				c.SetWriteDeadline(time.Now().Add(time.Second))
				if _, err := c.Write(b); err != nil {
					p.ledgerEvent("write_failed", seq)
					connected = false
					break
				}
				p.ledgerEvent("published", seq)
				p.mu.Lock()
				p.d.Egress++
				p.mu.Unlock()
			}
		}
		tick.Stop()
		c.Close()
	}
}

func (p *plantNode) relay() error {
	l, err := net.Listen("tcp", address(p.cfg.Base))
	if err != nil {
		return err
	}
	defer l.Close()
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		p.ledgerEvent("upstream_connected", 0)
		for {
			f, b, err := readFrame(c)
			if err != nil {
				break
			}
			if f.Run != p.m.Run || f.Feed != p.cfg.Chain[0] {
				break
			}
			p.mu.Lock()
			p.d.Ingress++
			p.lastReceive = time.Now()
			p.mu.Unlock()
			for _, q := range p.queues {
				q.push(b)
			}
		}
		c.Close()
	}
}

func (p *plantNode) dispatch(slot int) {
	l, err := net.Listen("tcp", address(p.cfg.Base+slot+1))
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	q := p.queues[slot]
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		p.ledgerEvent(fmt.Sprintf("subscriber_%d_connected", slot+1), 0)
		ack := make(chan struct{}, 32)
		done := make(chan struct{})
		go func() {
			defer close(done)
			var b [1]byte
			for {
				if _, err := io.ReadFull(c, b[:]); err != nil {
					return
				}
				if b[0] != 1 {
					return
				}
				select {
				case ack <- struct{}{}:
				case <-time.After(time.Second):
					return
				}
			}
		}()
		tick := time.NewTicker(time.Millisecond)
		credit := 0.0
		unacked := 0
	loop:
		for {
			select {
			case <-done:
				break loop
			case <-ack:
				if unacked == 0 {
					break loop
				}
				unacked--
				q.mu.Lock()
				q.unacked = unacked
				q.mu.Unlock()
			case <-tick.C:
				rate := p.control.Load().DispatchRate
				if rate == 0 {
					rate = 5000
				}
				credit += float64(rate) / 1000
				if credit > 5 {
					credit = 5
				}
				for credit >= 1 && unacked < 32 {
					b := q.pop()
					if b == nil {
						credit = 0
						break
					}
					credit--
					c.SetWriteDeadline(time.Now().Add(time.Second))
					if _, err := c.Write(b); err != nil {
						q.mu.Lock()
						q.drops++
						q.mu.Unlock()
						break loop
					}
					unacked++
					q.mu.Lock()
					q.sent++
					q.unacked = unacked
					q.mu.Unlock()
					p.mu.Lock()
					p.d.Egress++
					p.mu.Unlock()
				}
			}
		}
		tick.Stop()
		c.Close()
		<-done
		q.mu.Lock()
		q.unacked = 0
		q.mu.Unlock()
	}
}

func (p *plantNode) subscribe() error {
	var last, generation uint64
	for {
		c, err := net.DialTimeout("tcp", address(p.cfg.Base+p.cfg.Slot), time.Second)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		p.ledgerEvent("connected", last)
		p.mu.Lock()
		p.d.Reconnects++
		p.mu.Unlock()
		pending := make(chan uint64, 32)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for seq := range pending {
				if rate := p.control.Load().ConsumeRate; rate > 0 {
					time.Sleep(time.Second / time.Duration(rate))
				}
				p.mu.Lock()
				p.d.Consumed++
				p.pending = p.pending[1:]
				p.mu.Unlock()
				c.SetWriteDeadline(time.Now().Add(time.Second))
				if _, err := c.Write([]byte{1}); err != nil {
					c.Close()
					return
				}
				_ = seq
			}
		}()
		for {
			f, _, err := readFrame(c)
			if err != nil {
				if err.Error() == "corrupt frame" {
					p.mu.Lock()
					p.d.Corrupt++
					p.mu.Unlock()
				}
				break
			}
			if f.Run != p.m.Run || f.Feed != p.cfg.Chain[0] {
				p.mu.Lock()
				p.d.Corrupt++
				p.mu.Unlock()
				break
			}
			now := time.Now()
			p.ledgerEvent("received", f.Sequence)
			p.mu.Lock()
			if generation != f.Generation {
				generation = f.Generation
				last = 0
			}
			if last != 0 {
				if f.Sequence == last {
					p.d.Duplicates++
				} else if f.Sequence < last {
					p.d.OutOfOrder++
				} else if f.Sequence > last+1 {
					p.d.Missing += f.Sequence - last - 1
				}
			}
			if f.Sequence > last {
				last = f.Sequence
			}
			p.d.Ingress++
			p.lastReceive = now
			age := float64(now.UnixNano()-f.Source) / 1e9
			if age < 0 {
				p.d.ClockErrors++
			} else {
				p.d.AgeCount++
				p.d.AgeSum += age
				for i, bound := range ageBuckets {
					if age <= bound {
						p.d.AgeBuckets[i]++
					}
				}
			}
			p.pending = append(p.pending, now)
			p.mu.Unlock()
			select {
			case pending <- f.Sequence:
			case <-done:
				c.Close()
			}
		}
		c.Close()
		close(pending)
		<-done
		p.mu.Lock()
		p.pending = nil
		p.mu.Unlock()
	}
}

func (p *plantNode) telemetry() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	var seq uint64
	for range tick.C {
		seq++
		var ctl faultControl
		if readJSON(filepath.Join(p.dir, "control", p.cfg.ID+".json"), &ctl) == nil {
			p.control.Store(&ctl)
		}
		p.mu.Lock()
		d := p.d
		d.AgeBuckets = append([]uint64(nil), p.d.AgeBuckets...)
		d.LastReceiveAge = time.Since(p.lastReceive).Seconds()
		d.Pending = len(p.pending)
		if d.Pending > 0 {
			d.PendingAge = time.Since(p.pending[0]).Seconds()
		}
		p.mu.Unlock()
		if p.cfg.Role == "relay" {
			for i, q := range p.queues {
				d.Queues = append(d.Queues, q.snapshot(fmt.Sprintf("%s-sub-%d", p.cfg.Chain, i+1)))
			}
		}
		r := record{Schema: 1, Run: p.m.Run, Kind: "process", Process: p.cfg.ID, Host: "lab-host", Generation: p.cfg.Generation, Group: "delivery", Sequence: seq, Observed: time.Now().UTC(), Interval: 1000, Delivery: &d}
		batch := []record{r}
		if seq%5 == 0 {
			r.Group = "resources"
			r.Interval = 5000
			r.Sequence = seq / 5
			r.Delivery = nil
			resource := processResources()
			r.Resources = &resource
			batch = append(batch, r)
		}
		if ctl.TelemetryOff {
			p.mu.Lock()
			p.d.Unsent += uint64(len(batch))
			p.mu.Unlock()
			continue
		}
		if err := sendRecords(client, p.token, batch); err != nil {
			p.mu.Lock()
			p.d.Unsent += uint64(len(batch))
			p.mu.Unlock()
		}
	}
}

func processResources() resources {
	var r syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &r)
	v := resources{CPUSeconds: float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6}
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		f := strings.Fields(string(b))
		if len(f) > 1 {
			pages, _ := strconv.ParseUint(f[1], 10, 64)
			v.RSSBytes = pages * uint64(os.Getpagesize())
		}
	}
	fds, _ := os.ReadDir("/proc/self/fd")
	v.FDs = len(fds)
	return v
}

func hostResources() resources {
	var v resources
	b, _ := os.ReadFile("/proc/meminfo")
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			v.MemoryTotal = n * 1024
		case "MemAvailable:":
			v.MemoryAvailable = n * 1024
		}
	}
	b, _ = os.ReadFile("/proc/stat")
	if lines := strings.Split(string(b), "\n"); len(lines) > 0 {
		f := strings.Fields(lines[0])
		for i := 1; i < len(f) && i <= 8; i++ {
			if i == 4 || i == 5 {
				continue
			}
			n, _ := strconv.ParseFloat(f[i], 64)
			v.CPUSeconds += n / 100
		}
	}
	b, _ = os.ReadFile("/proc/net/dev")
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 17 && f[0] != "lo:" {
			rx, _ := strconv.ParseUint(f[1], 10, 64)
			tx, _ := strconv.ParseUint(f[9], 10, 64)
			v.NetworkReceive += rx
			v.NetworkTransmit += tx
		}
	}
	return v
}

func sendRecords(client *http.Client, token string, records []record) error {
	b, _ := json.Marshal(records)
	req, _ := http.NewRequest("POST", "http://127.0.0.1:18081/ingest", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 202 {
		return fmt.Errorf("collector returned %d", resp.StatusCode)
	}
	return nil
}
