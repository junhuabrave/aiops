package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sandboxSource(t *testing.T, s *taskStore) investigation {
	t.Helper()
	j := frozenFixture()
	// Make resource samples fresh too so both specialist types must return data.
	for i := range j.Samples {
		for n := range j.Samples[i].Evidence {
			j.Samples[i].Evidence[n].ResourceObserved = j.Samples[i].At.Add(-time.Second)
		}
	}
	if _, err := s.submit(context.Background(), j.ID, j.Run, j.Chain, j.Question); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(j.Samples)
	if _, err := s.db.Exec(`UPDATE investigations SET state='completed', samples=$2 WHERE id=$1`, j.ID, b); err != nil {
		t.Fatal(err)
	}
	return j
}

func admittedSandbox(t *testing.T, s *taskStore, source investigation, profile string) sandboxRun {
	t.Helper()
	if _, err := s.submitSandbox(context.Background(), randomID(16), source.ID, source.Run, profile); err != nil {
		t.Fatal(err)
	}
	j, err := s.claimSandbox(context.Background(), source.Run)
	if err != nil || j.ID == "" {
		t.Fatal("claim", j, err)
	}
	return j
}

func TestSandboxHTTPAndIsolatedReplay(t *testing.T) {
	s, _ := testTasks(t)
	source := sandboxSource(t, s)
	c, _ := fixture()
	c.manifest.Run = source.Run
	a := taskAPI(t, c, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.sandboxWork(ctx, source.Run) }()
	defer func() { cancel(); <-done }()
	for _, profile := range []string{"chain", "subscriber-1"} {
		id := randomID(16)
		path := "/api/sandbox-investigations/" + id
		body := fmt.Sprintf(`{"id":%q,"source_id":%q,"profile":%q}`, id, source.ID, profile)
		if w := (testAPI{h: a.h}).request("POST", "/api/sandbox-investigations", body); w.Code != 401 {
			t.Fatal("anonymous admission", w.Code)
		}
		if w := a.request("POST", "/api/sandbox-investigations", body); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		conflict := strings.Replace(body, `"source_id":"a`, `"source_id":"b`, 1)
		if w := a.request("POST", "/api/sandbox-investigations", conflict); w.Code != 409 {
			t.Fatal("conflicting retry", w.Code)
		}
		if w := (testAPI{h: a.h}).request("GET", path, ""); w.Code != 401 {
			t.Fatal("anonymous result", w.Code)
		}
		var j sandboxRun
		for until := time.Now().Add(20 * time.Second); time.Now().Before(until); {
			w := a.request("GET", path, "")
			if w.Code != 200 || decodeExact(w.Body.Bytes(), &j) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			if j.State != "queued" && j.State != "running" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if j.State != "completed" || j.Result == nil || j.Result.LiveModel || j.Calls != 9 || j.ReservedBytes != 3*(4096+128<<10) {
			t.Fatalf("isolated replay failed: %+v", j)
		}
		for role, points := range j.Result.Claims {
			if role == "integration" {
				if len(points) != 0 {
					t.Fatal("invented change evidence")
				}
				continue
			}
			if len(points) != 2 {
				t.Fatal("missing point claims", role, points)
			}
			for _, p := range points {
				if p.Availability != "fresh" || (profile == "subscriber-1" && p.Resource != "a-sub-1") {
					t.Fatal("scope/freshness leak", p)
				}
			}
		}
		before := a.request("GET", path, "").Body.String()
		if a.request("POST", path+"/cancel", "{}").Code != 200 || a.request("POST", "/api/sandbox-investigations", body).Code != 202 {
			t.Fatal("terminal idempotency failed")
		}
		if after := a.request("GET", path, "").Body.String(); before != after {
			t.Fatal("terminal result mutated")
		}
	}
	// Session and same-origin protections also apply to cancellation.
	if w := (testAPI{h: a.h}).request("POST", "/api/sandbox-investigations/"+randomID(16)+"/cancel", "{}"); w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous cancellation", w.Code)
	}
}

func TestSandboxDurableFencesAndReservations(t *testing.T) {
	s, dsn := testTasks(t)
	source := sandboxSource(t, s)
	ctx := context.Background()
	j := admittedSandbox(t, s, source, "chain")
	other, err := openTasks(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.db.Close()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = other
			}
			e := store.sandboxPermit(ctx, j.ID, j.Generation, 100, 128<<10)
			if e == nil {
				accepted.Add(1)
			} else if !errors.Is(e, errBudget) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 4 {
		t.Fatal("over/under-reserved", accepted.Load())
	}
	persisted, err := other.getSandbox(ctx, j.ID)
	if err != nil || persisted.Calls != 4 || persisted.ReservedBytes != 512<<10 || persisted.RequestBytes != 400 {
		t.Fatal("reservations not shared", persisted, err)
	}
	// Reopening a service connection neither resets reservations nor permits stale generations.
	fresh, err := openTasks(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.db.Close()
	if !errors.Is(fresh.sandboxPermit(ctx, j.ID, j.Generation+1, 0, 0), errFenced) {
		t.Fatal("generation accepted")
	}
	if !errors.Is(fresh.sandboxPermit(ctx, j.ID, j.Generation, 1, 1), errBudget) {
		t.Fatal("budget reset")
	}
	if _, err = s.cancelSandbox(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(other.sandboxPermit(ctx, j.ID, j.Generation, 0, 0), errFenced) {
		t.Fatal("read after cancellation")
	}
	if !errors.Is(other.updateSandbox(ctx, j, "completed", "late", &sandboxReport{}), errFenced) {
		t.Fatal("late publication")
	}
	for _, reason := range []string{"deadline", "lease", "run"} {
		j = admittedSandbox(t, s, source, "chain")
		run := source.Run
		switch reason {
		case "deadline":
			_, err = s.db.Exec(`UPDATE sandbox_runs SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID)
		case "lease":
			_, err = s.db.Exec(`UPDATE sandbox_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID)
		case "run":
			run = "replacement"
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = other.claimSandbox(ctx, run); err != nil {
			t.Fatal(err)
		}
		after, err := s.getSandbox(ctx, j.ID)
		if err != nil || (after.State != "expired" && after.State != "blocked") {
			t.Fatal("not terminal", after, err)
		}
		if !errors.Is(s.sandboxPermit(ctx, j.ID, j.Generation, 0, 0), errFenced) {
			t.Fatal("lost owner accepted")
		}
	}
}

func TestSandboxWorkerCannotEscapeOrForgeAuthority(t *testing.T) {
	s, _ := testTasks(t)
	source := sandboxSource(t, s)
	exported, _ := exportEvidence(source)
	var window frozenWindow
	if err := decodeExact([]byte(exported.Payload), &window); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script, filter := filepath.Join(dir, "probe.py"), filepath.Join(dir, "filter")
	if err := os.WriteFile(filter, sandboxFilter(), 0600); err != nil {
		t.Fatal(err)
	}
	// Only this fixed, evidence-free probe exposes startup stderr. Real worker
	// output remains untrusted and must not become an operator log channel.
	if err := os.WriteFile(script, []byte("print('sandbox ready')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd, fd, err := isolatedCommand(ctx, script, filter)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = nil
	startup, startupErr := cmd.CombinedOutput()
	fd.Close()
	if startupErr != nil || string(startup) != "sandbox ready\n" {
		t.Fatalf("trusted sandbox startup probe: %v\n%s", startupErr, startup)
	}
	sentinel := filepath.Join(dir, "private-operator-token")
	if err := os.WriteFile(sentinel, []byte("test-secret-not-for-worker"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLANT_TEST_SECRET", "should-not-be-inherited")
	// This probe runs inside the real sandbox; assertion failures fail the run.
	probe := fmt.Sprintf(`import ctypes, errno, json, os, socket, sys, resource
assignment = json.loads(sys.stdin.readline())
assert "PLANT_TEST_SECRET" not in os.environ
assert not os.path.exists(%q)
assert not os.path.exists("/home/user") and not os.path.exists("/etc")
assert not os.path.exists("/proc") and not os.path.exists("/dev")
assert resource.getrlimit(resource.RLIMIT_AS)[0] == 268435456
def denied(fn):
    try: fn()
    except OSError: return
    raise AssertionError("sandbox escape succeeded")
denied(lambda: socket.socket(socket.AF_INET, socket.SOCK_STREAM))
denied(lambda: socket.socket(socket.AF_UNIX, socket.SOCK_STREAM))
denied(lambda: open("/worker.py", "w"))
denied(lambda: os.fstat(3))
denied(lambda: open("/unexpected", "w"))
denied(lambda: open("/dev/unexpected", "w"))
assert os.statvfs("/tmp").f_blocks * os.statvfs("/tmp").f_frsize <= 1048576
denied(os.fork)
libc = ctypes.CDLL(None, use_errno=True)
assert libc.syscall(99, ctypes.create_string_buffer(256)) == -1 and ctypes.get_errno() == errno.EPERM
assert libc.syscall(250, 0, 0, 0, 0, 0) == -1 and ctypes.get_errno() == errno.EPERM
print(json.dumps({"kind":"tool","tool":"delivery_window","resources":[]}), flush=True)
r = json.loads(sys.stdin.readline())
assert all(p["resource"] == "a-sub-1" for p in r["points"])
p = next(p for p in r["points"] if p["metric"] == "ingress")
assert p["value"] == 9007199254740993
print(json.dumps({"kind":"finish","evidence_ids":[p["id"]]}), flush=True)
`, sentinel)
	if err := os.WriteFile(script, []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	j := admittedSandbox(t, s, source, "subscriber-1")
	points, err := s.specialist(context.Background(), j, window, "operations", script, filter)
	if err != nil || len(points) != 1 || points[0].Value.(json.Number).String() != "9007199254740993" {
		t.Fatal("isolation or exact evidence check failed", points, err)
	}
	s.cancelSandbox(context.Background(), j.ID)
	for name, message := range map[string]string{
		"role spoof":        `{"kind":"tool","role":"engineering","tool":"resource_window"}`,
		"role tool":         `{"kind":"tool","tool":"resource_window"}`,
		"cross scope":       `{"kind":"tool","tool":"delivery_window","resources":["a-relay"]}`,
		"cross chain":       `{"kind":"tool","tool":"delivery_window","resources":["b-sub-1"]}`,
		"provider":          `{"kind":"tool","tool":"model"}`,
		"invented citation": `{"kind":"finish","evidence_ids":["2/a-sub-1/ingress/"]}`,
		"factual prose":     `{"kind":"finish","evidence_ids":[],"root_cause":"CPU"}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := "import sys\nsys.stdin.readline()\nprint(" + fmt.Sprintf("%q", message) + ",flush=True)\nsys.stdin.readline()\n"
			if err := os.WriteFile(script, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			j := admittedSandbox(t, s, source, "subscriber-1")
			defer s.cancelSandbox(context.Background(), j.ID)
			if _, err := s.specialist(context.Background(), j, window, "operations", script, filter); err == nil {
				t.Fatal("worker escaped contract")
			}
		})
	}
}

func TestSandboxSharedRelayAndSelection(t *testing.T) {
	exported, _ := exportEvidence(frozenFixture())
	var window frozenWindow
	if err := decodeExact([]byte(exported.Payload), &window); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"delivery_window", "telemetry_health", "queue_window"} {
		for _, scope := range [][]string{{"a-relay"}, {"a-relay", "a-sub-1"}, {"a-relay", "a-sub-1", "a-sub-2"}} {
			out, err := sandboxTool(window, "operations", scope, workerRequest{Kind: "tool", Tool: tool, Resources: []string{"a-relay"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range out.Points {
				if p.Peer != "" {
					t.Fatal("peer not selected")
				}
				if len(scope) < 3 && (p.Metric == "ingress" || p.Metric == "egress") {
					t.Fatal("relay total leaked")
				}
			}
			for _, p := range out.Health {
				if len(scope) < 3 && (p["metric"] == "ingress" || p["metric"] == "egress") {
					t.Fatal("health total leaked")
				}
			}
			if tool == "delivery_window" && len(scope) == 3 && len(out.Points) != 18 {
				t.Fatal("authorized totals withheld")
			}
		}
	}
	delivered := map[string]evidencePoint{}
	for _, p := range window.Points {
		delivered[p.ID] = p
	}
	for _, ids := range [][]string{{"0/a-publisher/rss_bytes/"}, {"0/a-sub-2/ingress/"}, {"0/a-publisher/ingress/", "0/a-publisher/ingress/"}} {
		if _, err := sandboxSelection(workerRequest{Kind: "finish", EvidenceIDs: ids}, delivered); err == nil {
			t.Fatal("stale/missing/duplicate accepted", ids)
		}
	}
}

func TestSandboxStopsSilentWorkerAndKeepsCharges(t *testing.T) {
	s, _ := testTasks(t)
	source := sandboxSource(t, s)
	ctx := context.Background()
	j := admittedSandbox(t, s, source, "subscriber-1")
	done := make(chan error, 1)
	go func() {
		done <- s.executeSandbox(ctx, j, []byte(`import sys,time,json
sys.stdin.readline()
print(json.dumps({"kind":"tool","tool":"delivery_window"}),flush=True)
sys.stdin.readline()
time.sleep(60)
`))
	}()
	for until := time.Now().Add(5 * time.Second); ; {
		current, err := s.getSandbox(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Calls == 2 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("worker never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.cancelSandbox(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled execution succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("silent worker survived cancellation watchdog")
	}
	after, err := s.getSandbox(ctx, j.ID)
	if err != nil || after.State != "cancelled" || after.Result != nil || after.Calls != 2 || after.ReservedBytes != 4096+128<<10 {
		t.Fatal("cancellation lost charges or published a result", after, err)
	}
}

func TestSandboxAdmissionAndCallLimitsAcrossServices(t *testing.T) {
	s, dsn := testTasks(t)
	source := sandboxSource(t, s)
	ctx := context.Background()
	other, err := openTasks(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.db.Close()
	var wg sync.WaitGroup
	var admitted, claimed atomic.Int32
	for i := range 24 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := []*taskStore{s, other}[i%2]
			if _, e := store.submitSandbox(ctx, randomID(16), source.ID, source.Run, "chain"); e == nil {
				admitted.Add(1)
			} else if !errors.Is(e, errQueueFull) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if admitted.Load() != 20 {
		t.Fatal("queue limit", admitted.Load())
	}
	owners := make(chan sandboxRun, 24)
	for i := range 24 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j, e := []*taskStore{s, other}[i%2].claimSandbox(ctx, source.Run)
			if e != nil {
				t.Error(e)
			} else if j.ID != "" {
				claimed.Add(1)
				owners <- j
			}
		}(i)
	}
	wg.Wait()
	close(owners)
	if claimed.Load() != 2 {
		t.Fatal("active limit", claimed.Load())
	}
	for j := range owners {
		for range 12 {
			if err := s.sandboxPermit(ctx, j.ID, j.Generation, 1, 0); err != nil {
				t.Fatal(err)
			}
		}
		if !errors.Is(other.sandboxPermit(ctx, j.ID, j.Generation, 1, 0), errBudget) {
			t.Fatal("call cap not shared")
		}
	}
}
