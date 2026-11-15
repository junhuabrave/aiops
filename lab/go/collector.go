package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

type samplePair struct {
	Current  record
	Previous record
}
type collector struct {
	mu                                sync.RWMutex
	manifest                          manifest
	token                             string
	samples                           map[string]samplePair
	accepted, ignored, rejected, gaps uint64
	sessions                          map[string]bool
}

func newCollector(m manifest, token string) *collector {
	return &collector{manifest: m, token: token, samples: make(map[string]samplePair), sessions: map[string]bool{"a": true, "b": true}}
}

func key(r record) string { return r.Kind + "/" + r.Process + "/" + r.Host + "/" + r.Group }

func (c *collector) validate(r record) error {
	if r.Schema != 1 || r.Run != c.manifest.Run || r.Host != "lab-host" || r.Sequence == 0 || r.Observed.Before(c.manifest.Started) || r.Observed.After(time.Now().Add(2*time.Second)) {
		return fmt.Errorf("invalid identity or observation")
	}
	role, chain := "", ""
	if r.Kind == "host" {
		if r.Process != "" || r.Generation != c.manifest.HostGeneration || r.Group != "resources" {
			return fmt.Errorf("invalid host identity")
		}
	} else if r.Kind == "process" {
		for _, n := range c.manifest.Nodes {
			if n.ID == r.Process && n.Generation == r.Generation {
				role, chain = n.Role, n.Chain
			}
		}
		if role == "" {
			return fmt.Errorf("unknown process generation")
		}
	} else {
		return fmt.Errorf("invalid resource kind")
	}
	switch r.Group {
	case "delivery":
		if r.Kind != "process" || r.Interval != 1000 || r.Delivery == nil || r.Resources != nil {
			return fmt.Errorf("invalid delivery group")
		}
		d := r.Delivery
		if len(d.AgeBuckets) != len(ageBuckets) || d.Pending < 0 || d.Pending > 32 || d.PendingAge < 0 || d.LastReceiveAge < 0 || d.AgeSum < 0 {
			return fmt.Errorf("invalid delivery values")
		}
		for i, n := range d.AgeBuckets {
			if n > d.AgeCount || (i > 0 && n < d.AgeBuckets[i-1]) {
				return fmt.Errorf("invalid histogram")
			}
		}
		if role == "relay" {
			if len(d.Queues) != 2 {
				return fmt.Errorf("missing queues")
			}
		} else if len(d.Queues) != 0 {
			return fmt.Errorf("unexpected queues")
		}
		for i, q := range d.Queues {
			if q.Subscriber != fmt.Sprintf("%s-sub-%d", chain, i+1) || q.Depth < 0 || q.Depth > 1000 || q.Maximum < q.Depth || q.Maximum > 1000 || q.Unacked < 0 || q.Unacked > 32 || q.OldestSeconds < 0 {
				return fmt.Errorf("invalid queue")
			}
		}
	case "resources":
		if r.Interval != 5000 || r.Resources == nil || r.Delivery != nil || r.Resources.CPUSeconds < 0 || r.Resources.FDs < 0 {
			return fmt.Errorf("invalid resource group")
		}
	default:
		return fmt.Errorf("unknown metric group")
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

func (c *collector) ingest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.token)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	var batch []record
	err := decodeJSON(w, r, &batch, 256<<10)
	if err == nil && (len(batch) == 0 || len(batch) > 100) {
		err = fmt.Errorf("batch must contain 1..100 records")
	}
	if err == nil {
		for _, rec := range batch {
			if err = c.validate(rec); err != nil {
				break
			}
		}
	}
	if err != nil {
		c.mu.Lock()
		c.rejected++
		c.mu.Unlock()
		http.Error(w, err.Error(), 400)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rec := range batch {
		k := key(rec)
		prev, ok := c.samples[k]
		if ok && (rec.Sequence <= prev.Current.Sequence || rec.Observed.Before(prev.Current.Observed)) {
			c.ignored++
			continue
		}
		if ok && rec.Sequence > prev.Current.Sequence+1 {
			c.gaps += rec.Sequence - prev.Current.Sequence - 1
		}
		c.samples[k] = samplePair{rec, prev.Current}
		c.accepted++
	}
	w.WriteHeader(http.StatusAccepted)
}

type nodeView struct {
	ID               string     `json:"id"`
	Role             string     `json:"role"`
	Chain            string     `json:"chain"`
	Generation       string     `json:"generation"`
	ResourceObserved time.Time  `json:"resource_observed_at"`
	State            string     `json:"state"`
	Age              float64    `json:"observation_age_seconds"`
	ResourceAge      float64    `json:"resource_age_seconds"`
	Rate             *float64   `json:"rate_per_second"`
	Observed         time.Time  `json:"observed_at"`
	SampleSequence   uint64     `json:"sample_seq"`
	Delivery         *delivery  `json:"delivery"`
	Resources        *resources `json:"resources"`
}
type plantView struct {
	Run      string          `json:"run_id"`
	At       time.Time       `json:"at"`
	Nodes    []nodeView      `json:"nodes"`
	Sessions map[string]bool `json:"active_sessions"`
	Host     *resources      `json:"host"`
	HostAge  float64         `json:"host_age_seconds"`
}

func (c *collector) view() plantView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := plantView{Run: c.manifest.Run, At: time.Now().UTC(), Sessions: map[string]bool{}, HostAge: -1}
	for chain, active := range c.sessions {
		v.Sessions[chain] = active
	}
	for _, n := range c.manifest.Nodes {
		x := nodeView{ID: n.ID, Role: n.Role, Chain: n.Chain, Generation: n.Generation, State: "missing", Age: -1, ResourceAge: -1}
		p, ok := c.samples["process/"+n.ID+"/lab-host/delivery"]
		if ok {
			x.Age = time.Since(p.Current.Observed).Seconds()
			x.Observed = p.Current.Observed
			x.SampleSequence = p.Current.Sequence
			x.Delivery = p.Current.Delivery
			x.State = "healthy"
			if x.Age > 3 {
				x.State = "stale"
			} else {
				if n.Role == "subscriber" && ((x.Delivery.LastReceiveAge > 3 && c.sessions[n.Chain]) || x.Delivery.PendingAge > .1) {
					x.State = "symptom"
				}
				for _, q := range x.Delivery.Queues {
					if q.OldestSeconds > 2 {
						x.State = "symptom"
					}
				}
				if p.Previous.Delivery != nil {
					a, b := p.Current.Delivery.Ingress, p.Previous.Delivery.Ingress
					if n.Role == "publisher" {
						a, b = p.Current.Delivery.Egress, p.Previous.Delivery.Egress
					}
					if dt := p.Current.Observed.Sub(p.Previous.Observed).Seconds(); dt > 0 && a >= b {
						rate := float64(a-b) / dt
						x.Rate = &rate
					}
				}
			}
		}
		if resource, ok := c.samples["process/"+n.ID+"/lab-host/resources"]; ok {
			x.ResourceAge = time.Since(resource.Current.Observed).Seconds()
			x.ResourceObserved = resource.Current.Observed
			x.Resources = resource.Current.Resources
		}
		v.Nodes = append(v.Nodes, x)
	}
	if h, ok := c.samples["host//lab-host/resources"]; ok {
		v.Host = h.Current.Resources
		v.HostAge = time.Since(h.Current.Observed).Seconds()
	}
	return v
}

func (c *collector) metrics(group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		c.mu.RLock()
		defer c.mu.RUnlock()
		emit := func(name, labels string, value any) { fmt.Fprintf(w, "plant_%s{%s} %v\n", name, labels, value) }
		if group == "delivery" {
			fmt.Fprintf(w, "plant_collector_accepted_total %d\nplant_collector_ignored_total %d\nplant_collector_rejected_total %d\nplant_collector_sequence_gaps_total %d\n", c.accepted, c.ignored, c.rejected, c.gaps)
		}
		for _, n := range c.manifest.Nodes {
			labels := fmt.Sprintf("run_id=%q,process=%q,role=%q,chain=%q,group=%q,generation=%q", c.manifest.Run, n.ID, n.Role, n.Chain, group, n.Generation)
			emit("expected", labels, 1)
			if group == "delivery" {
				active := 0
				if c.sessions[n.Chain] {
					active = 1
				}
				emit("session_active", labels, active)
			}
		}
		if group == "resources" {
			emit("expected", fmt.Sprintf("run_id=%q,host=%q,group=%q,generation=%q", c.manifest.Run, "lab-host", group, c.manifest.HostGeneration), 1)
		}
		keys := make([]string, 0, len(c.samples))
		for k := range c.samples {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			x := c.samples[k].Current
			if x.Group != group {
				continue
			}
			labels := fmt.Sprintf("run_id=%q,host=%q,group=%q,generation=%q", x.Run, x.Host, group, x.Generation)
			if x.Kind == "process" {
				for _, n := range c.manifest.Nodes {
					if n.ID == x.Process {
						labels = fmt.Sprintf("run_id=%q,process=%q,role=%q,chain=%q,group=%q,generation=%q", x.Run, n.ID, n.Role, n.Chain, group, x.Generation)
					}
				}
			}
			emit("observation_timestamp_seconds", labels, float64(x.Observed.UnixNano())/1e9)
			emit("sample_sequence", labels, x.Sequence)
			if d := x.Delivery; d != nil {
				for name, val := range map[string]any{"ingress_total": d.Ingress, "egress_total": d.Egress, "consumed_total": d.Consumed, "missing_total": d.Missing, "duplicates_total": d.Duplicates, "out_of_order_total": d.OutOfOrder, "corrupt_total": d.Corrupt, "reconnects_total": d.Reconnects, "receive_age_seconds": d.LastReceiveAge, "pending": d.Pending, "pending_age_seconds": d.PendingAge, "unsent_snapshots_total": d.Unsent, "clock_errors_total": d.ClockErrors} {
					emit(name, labels, val)
				}
				for _, q := range d.Queues {
					l := labels + fmt.Sprintf(",subscriber=%q", q.Subscriber)
					for name, val := range map[string]any{"queue_depth": q.Depth, "queue_maximum": q.Maximum, "queue_age_seconds": q.OldestSeconds, "queue_drops_total": q.Drops, "queue_sent_total": q.Sent, "unacked": q.Unacked} {
						emit(name, l, val)
					}
				}
				if d.AgeBuckets != nil {
					for i, b := range ageBuckets {
						emit("message_age_seconds_bucket", labels+fmt.Sprintf(",le=%q", strconv.FormatFloat(b, 'g', -1, 64)), d.AgeBuckets[i])
					}
					emit("message_age_seconds_bucket", labels+",le=\"+Inf\"", d.AgeCount)
					emit("message_age_seconds_count", labels, d.AgeCount)
					emit("message_age_seconds_sum", labels, d.AgeSum)
				}
			}
			if v := x.Resources; v != nil {
				if x.Kind == "process" {
					emit("cpu_seconds_total", labels, v.CPUSeconds)
					emit("rss_bytes", labels, v.RSSBytes)
					emit("file_descriptors", labels, v.FDs)
				} else {
					emit("host_cpu_busy_seconds_total", labels, v.CPUSeconds)
					emit("host_memory_total_bytes", labels, v.MemoryTotal)
					emit("host_memory_available_bytes", labels, v.MemoryAvailable)
					emit("host_network_receive_bytes_total", labels, v.NetworkReceive)
					emit("host_network_transmit_bytes_total", labels, v.NetworkTransmit)
				}
			}
		}
	}
}
