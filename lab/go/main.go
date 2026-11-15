package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

func randomID(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: plantlab up|down|node|collector|journal|scenario")
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dir := fs.String("dir", ".run/current", "run directory")
	port := fs.Int("port", 8080, "console port")
	id := fs.String("id", "", "process identity")
	scenario := fs.String("case", "healthy", "scenario")
	chain := fs.String("chain", "a", "chain a or b")
	fs.Parse(os.Args[2:])
	var err error
	switch os.Args[1] {
	case "up":
		err = launch(*port)
	case "node":
		err = runNode(*dir, *id)
	case "collector":
		err = runCollector(*dir, *port)
	case "journal":
		err = runJournal(*dir)
	case "scenario":
		err = applyScenario(*dir, *scenario, *chain)
	case "down":
		client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", ".run/stop.sock")
		}}, Timeout: time.Second}
		var resp *http.Response
		resp, err = client.Post("http://unix/stop", "text/plain", nil)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != 202 {
				err = fmt.Errorf("stop refused")
			}
		}
	default:
		err = fmt.Errorf("unknown command")
	}
	if err != nil {
		log.Fatal(err)
	}
}

func launch(port int) error {
	if _, err := os.Stat("config/prometheus.yml"); err != nil {
		return fmt.Errorf("run from lab/ (make -C lab up)")
	}
	if err := os.MkdirAll(".run", 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(".run/lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return fmt.Errorf("lab already running")
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	m := manifest{Run: randomID(8), Started: time.Now().UTC(), HostGeneration: strings.TrimSpace(string(bootID)), Versions: map[string]string{"go": runtime.Version(), "grafana": "12.3.0", "protocol": "1", "mode": "deterministic-only"}}
	for i, chain := range []string{"a", "b"} {
		for slot, role := range []string{"relay", "subscriber", "subscriber", "publisher"} {
			id := chain + "-" + role
			if role == "subscriber" {
				id = fmt.Sprintf("%s-sub-%d", chain, slot)
			}
			m.Nodes = append(m.Nodes, nodeConfig{id, role, chain, 12000 + i*100, slot, randomID(8), uint64(time.Now().UnixNano())})
		}
	}
	dir, _ := filepath.Abs(filepath.Join(".run", m.Run))
	if err = os.MkdirAll(filepath.Join(dir, "control"), 0700); err != nil {
		return err
	}
	if err = writeJSON(filepath.Join(dir, "manifest.json"), m); err != nil {
		return err
	}
	for _, name := range []string{"collector.token", "operator.token"} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte(randomID(32)), 0600); err != nil {
			return err
		}
	}
	for _, n := range m.Nodes {
		if err = writeJSON(filepath.Join(dir, "control", n.ID+".json"), faultControl{}); err != nil {
			return err
		}
	}
	if err = writeJSON(filepath.Join(dir, "sessions.json"), map[string]bool{"a": true, "b": true}); err != nil {
		return err
	}
	os.Remove(".run/current") // Symlink only; refuses a nonempty directory.
	if err = os.Symlink(m.Run, ".run/current"); err != nil {
		return err
	}
	exe, _ := os.Executable()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var commands []*exec.Cmd
	var wg sync.WaitGroup
	exited := make(chan error, 16)
	defer func() {
		cancel()
		for _, cmd := range commands {
			cmd.Process.Signal(syscall.SIGTERM)
		}
		wg.Wait()
	}()
	start := func(name, binary string, args ...string) error {
		f, e := os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdout = f
		cmd.Stderr = f
		if e = cmd.Start(); e != nil {
			f.Close()
			return e
		}
		commands = append(commands, cmd)
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := cmd.Wait()
			f.Close()
			select {
			case exited <- fmt.Errorf("%s exited: %v (see run log)", name, e):
			default:
			}
		}()
		return nil
	}
	for name, binary := range map[string]string{"prometheus": "prometheus", "alertmanager": "prometheus-alertmanager"} {
		v, e := exec.Command(binary, "--version").CombinedOutput()
		if e != nil {
			return e
		}
		m.Versions[name] = strings.Split(string(v), "\n")[0]
	}
	if err = writeJSON(filepath.Join(dir, "manifest.json"), m); err != nil {
		return err
	}
	databaseDir := labDatabaseDir(dir)
	if err = start("postgres", "bash", "postgres.sh", databaseDir); err != nil {
		return err
	}
	ready := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if exec.CommandContext(ctx, "/usr/lib/postgresql/17/bin/pg_isready", "-h", databaseDir, "-p", "15432", "-U", "plantlab", "-d", "postgres").Run() == nil {
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-exited:
			return e
		case <-time.After(200 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("private PostgreSQL did not become ready; see postgres.log")
	}
	if err = start("journal", exe, "journal", "-dir", dir); err != nil {
		return err
	}
	if err = start("collector", exe, "collector", "-dir", dir, "-port", fmt.Sprint(port)); err != nil {
		return err
	}
	if err = start("prometheus", "prometheus", "--config.file=config/prometheus.yml", "--storage.tsdb.path="+filepath.Join(dir, "prometheus"), "--storage.tsdb.retention.time=24h", "--web.listen-address=127.0.0.1:19090"); err != nil {
		return err
	}
	if err = start("alertmanager", "prometheus-alertmanager", "--config.file=config/alertmanager.yml", "--storage.path="+filepath.Join(dir, "alertmanager"), "--web.listen-address=127.0.0.1:19093", "--cluster.listen-address="); err != nil {
		return err
	}
	for _, n := range m.Nodes {
		if err = start(n.ID, exe, "node", "-dir", dir, "-id", n.ID); err != nil {
			return err
		}
	}
	if err = startGrafana(dir, start); err != nil {
		return err
	}
	os.Remove(".run/stop.sock")
	listener, err := net.Listen("unix", ".run/stop.sock")
	if err != nil {
		return err
	}
	defer listener.Close()
	os.Chmod(".run/stop.sock", 0600)
	stopServer := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/stop" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(202)
		cancel()
	})}
	go stopServer.Serve(listener)
	log.Printf("run %s; console port %d; login token file %s/operator.token; live AI disabled", m.Run, port, dir)
	select {
	case <-ctx.Done():
		return nil
	case e := <-exited:
		return e
	}
}

func startGrafana(dir string, start func(string, string, ...string) error) error {
	home, err := filepath.Abs(".tools/grafana")
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(home, "bin/grafana")); err != nil {
		return fmt.Errorf("Grafana missing; run bash lab/install.sh from repo root")
	}
	config, _ := filepath.Abs("config/grafana")
	ini := "[server]\nhttp_addr = 127.0.0.1\nhttp_port = 13000\nroot_url = %(protocol)s://%(domain)s/grafana/\nserve_from_sub_path = true\n[security]\nadmin_user = disabled-admin\nadmin_password = " + randomID(32) + "\n[auth.anonymous]\nenabled = true\norg_role = Viewer\n[auth]\ndisable_login_form = true\n[analytics]\nreporting_enabled = false\ncheck_for_updates = false\n[plugins]\npreinstall_disabled = true\n[paths]\ndata = " + dir + "/grafana-data\nlogs = " + dir + "/grafana-logs\nprovisioning = " + config + "\n"
	if err = os.WriteFile(filepath.Join(dir, "grafana.ini"), []byte(ini), 0600); err != nil {
		return err
	}
	return start("grafana", filepath.Join(home, "bin/grafana"), "server", "--homepath", home, "--config", filepath.Join(dir, "grafana.ini"))
}

func runCollector(dir string, port int) error {
	var m manifest
	if err := readJSON(filepath.Join(dir, "manifest.json"), &m); err != nil {
		return err
	}
	token, err := os.ReadFile(filepath.Join(dir, "collector.token"))
	if err != nil {
		return err
	}
	c := newCollector(m, string(token))
	private := http.NewServeMux()
	private.HandleFunc("/ingest", c.ingest)
	private.HandleFunc("/metrics/fast", c.metrics("delivery"))
	private.HandleFunc("/metrics/resources", c.metrics("resources"))
	go func() {
		if err := (&http.Server{Addr: address(18081), Handler: private, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxHeaderBytes: 8192}).ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		var seq uint64
		for range tick.C {
			var sessions map[string]bool
			if readJSON(filepath.Join(dir, "sessions.json"), &sessions) == nil {
				c.mu.Lock()
				c.sessions = sessions
				c.mu.Unlock()
			}
			seq++
			if seq%5 != 0 {
				continue
			}
			v := hostResources()
			r := record{Schema: 1, Run: m.Run, Kind: "host", Host: "lab-host", Generation: m.HostGeneration, Group: "resources", Sequence: seq / 5, Observed: time.Now().UTC(), Interval: 5000, Resources: &v}
			_ = sendRecords(&http.Client{Timeout: 200 * time.Millisecond}, string(token), []record{r})
		}
	}()
	return serveConsole(c, dir, port)
}

func runJournal(dir string) error {
	f, err := os.OpenFile(filepath.Join(dir, "alerts.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
		if err != nil || !json.Valid(b) {
			http.Error(w, "invalid alert", 400)
			return
		}
		row, _ := json.Marshal(map[string]any{"received_at": time.Now().UTC(), "notification": json.RawMessage(b)})
		mu.Lock()
		defer mu.Unlock()
		if _, err = f.Write(append(row, '\n')); err == nil {
			err = f.Sync()
		}
		if err != nil {
			http.Error(w, "journal unavailable", 503)
			return
		}
		w.WriteHeader(202)
	})
	return (&http.Server{Addr: address(18082), Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second}).ListenAndServe()
}

func applyScenario(dir, name, chain string) error {
	if chain != "a" && chain != "b" {
		return fmt.Errorf("chain must be a or b")
	}
	var m manifest
	if err := readJSON(filepath.Join(dir, "manifest.json"), &m); err != nil {
		return err
	}
	controls := map[string]faultControl{}
	for _, n := range m.Nodes {
		controls[n.ID] = faultControl{}
	}
	sessions := map[string]bool{"a": true, "b": true}
	switch name {
	case "healthy":
	case "slow-consumer":
		controls[chain+"-sub-1"] = faultControl{ConsumeRate: 100}
	case "relay-bottleneck":
		controls[chain+"-relay"] = faultControl{DispatchRate: 250}
	case "upstream-interruption":
		controls[chain+"-publisher"] = faultControl{Pause: true}
	case "telemetry-failure":
		controls[chain+"-relay"] = faultControl{TelemetryOff: true}
	case "healthy-busy":
		for _, ch := range []string{"a", "b"} {
			controls[ch+"-publisher"] = faultControl{Rate: 1000}
		}
	case "quiet-session":
		for _, ch := range []string{"a", "b"} {
			controls[ch+"-publisher"] = faultControl{Pause: true}
			sessions[ch] = false
		}
	case "quiet-telemetry-failure":
		for _, ch := range []string{"a", "b"} {
			controls[ch+"-publisher"] = faultControl{Pause: true}
			sessions[ch] = false
		}
		controls[chain+"-relay"] = faultControl{TelemetryOff: true}
	default:
		return fmt.Errorf("unsupported scenario %q", name)
	}
	if err := writeJSON(filepath.Join(dir, "sessions.json"), sessions); err != nil {
		return err
	}
	for id, ctl := range controls {
		if err := writeJSON(filepath.Join(dir, "control", id+".json"), ctl); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, "harness.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	json.NewEncoder(f).Encode(map[string]any{"at": time.Now().UTC(), "case": name, "chain": chain})
	f.Sync()
	fmt.Printf("Harness applied %s to run %s (chain %s); verify observed effects independently.\n", name, m.Run, chain)
	return nil
}
