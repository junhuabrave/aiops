package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Every lifecycle test owns a disposable real PostgreSQL instance, never the lab
// or an environment-provided database. Missing PostgreSQL is a failure, not a skip.
func testTasks(t *testing.T) (*taskStore, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "plant-tasks-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	var cred *syscall.Credential
	if os.Geteuid() == 0 {
		u, e := user.Lookup("postgres")
		if e != nil {
			t.Fatal(e)
		}
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		cred = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
		if e = os.Chown(dir, uid, gid); e != nil {
			t.Fatal(e)
		}
	}
	command := func(binary string, args ...string) *exec.Cmd {
		cmd := exec.Command("/usr/lib/postgresql/17/bin/"+binary, args...)
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
		return cmd
	}
	if b, e := command("initdb", "-D", dir, "-U", "plantlab", "--auth-local=trust", "--auth-host=reject", "--no-instructions").CombinedOutput(); e != nil {
		t.Fatalf("initdb: %v\n%s", e, b)
	}
	logFile, err := os.Create(filepath.Join(dir, "test.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := command("postgres", "-D", dir, "-k", dir, "-h", "", "-p", "15432", "-c", "shared_buffers=16MB")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Signal(syscall.SIGINT); cmd.Wait(); logFile.Close() })
	dsn := databaseURL(dir)
	var s *taskStore
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s, err = openTasks(ctx, dsn)
		cancel()
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s, dsn
}

type testAPI struct {
	h      http.Handler
	cookie *http.Cookie
}

func taskAPI(t *testing.T, c *collector, s *taskStore) testAPI {
	t.Helper()
	a := testAPI{h: consoleHandler(c, s, "test-operator")}
	w := a.request("POST", "/api/login", `{"token":"test-operator"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	a.cookie = w.Result().Cookies()[0]
	return a
}

func (a testAPI) request(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://lab.test"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://lab.test")
	r.Header.Set("Content-Type", "application/json")
	if a.cookie != nil {
		r.AddCookie(a.cookie)
	}
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return w
}

func (a testAPI) submit(id, chain string) *httptest.ResponseRecorder {
	return a.request("POST", "/api/investigations", fmt.Sprintf(`{"id":%q,"chain":%q,"question":"Check delivery"}`, id, chain))
}

func apiTask(t *testing.T, a testAPI, id string) investigation {
	t.Helper()
	w := a.request("GET", "/api/investigations/"+id, "")
	var j investigation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &j) != nil {
		t.Fatalf("read task: %d %s", w.Code, w.Body.String())
	}
	return j
}

func TestInvestigationAdmissionCancellationAndRecovery(t *testing.T) {
	s, dsn := testTasks(t)
	c, _ := fixture()
	a := taskAPI(t, c, s)
	ctx := context.Background()
	other, err := openTasks(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.db.Close()
	b := taskAPI(t, c, other)
	unauthorized := testAPI{h: a.h}
	for _, path := range []string{"/api/investigations", "/api/investigations/unknown", "/api/events"} {
		if w := unauthorized.request("GET", path, ""); w.Code != 401 {
			t.Fatalf("unprotected %s", path)
		}
	}
	var wg sync.WaitGroup
	codes := make(chan int, 40)
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := a
			if i%2 != 0 {
				client = b
			}
			codes <- client.submit(fmt.Sprintf("%032x", i), "a").Code
		}(i)
	}
	wg.Wait()
	close(codes)
	accepted, rejected := 0, 0
	for code := range codes {
		switch code {
		case 202:
			accepted++
		case 429:
			rejected++
		default:
			t.Fatalf("unexpected admission %d", code)
		}
	}
	if accepted != 20 || rejected != 20 {
		t.Fatalf("admitted=%d rejected=%d", accepted, rejected)
	}
	claims := make(chan investigation, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 != 0 {
				store = other
			}
			j, e := store.claim(ctx, c.manifest.Run)
			if e == nil {
				claims <- j
			} else if !errors.Is(e, sql.ErrNoRows) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	close(claims)
	var running []investigation
	for j := range claims {
		running = append(running, j)
	}
	if len(running) != 2 || running[0].ID == running[1].ID {
		t.Fatalf("global active limit violated: %+v", running)
	}
	for range 2 {
		if w := a.submit(randomID(16), "b"); w.Code != 202 {
			t.Fatal("active slots incorrectly consumed queued capacity", w.Body.String())
		}
	}
	if w := b.submit(randomID(16), "b"); w.Code != 429 || w.Header().Get("Retry-After") != "5" {
		t.Fatal("queue boundary did not return bounded overload", w.Code)
	}
	j := running[0]
	if w := b.submit(j.ID, "a"); w.Code != 202 {
		t.Fatal("idempotent retry failed", w.Body.String())
	}
	if w := b.submit(j.ID, "b"); w.Code != 409 {
		t.Fatal("request ID changed scope")
	}
	if _, err = s.observe(ctx, j.ID, j.Generation, c.view); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if w := b.request("POST", "/api/investigations/"+j.ID+"/cancel", "{}"); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if _, err = s.observe(ctx, j.ID, j.Generation, func() plantView { t.Fatal("cancelled task read evidence"); return plantView{} }); !errors.Is(err, errFenced) {
		t.Fatal("late worker result accepted", err)
	}
	got := apiTask(t, a, j.ID)
	if got.State != "cancelled" || len(got.Samples) != 1 || got.Result != nil {
		t.Fatalf("bad cancelled result: %+v", got)
	}
	jobs, err := s.list(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, queued := range jobs {
		if queued.State == "queued" {
			if w := a.request("POST", "/api/investigations/"+queued.ID+"/cancel", "{}"); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
		}
	}
	// A fresh service connection must recover the same identity and retained evidence.
	j = running[1]
	if _, err = s.observe(ctx, j.ID, j.Generation, c.view); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE investigations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err = other.maintain(ctx, c.manifest.Run); err != nil {
		t.Fatal(err)
	}
	recovered, err := other.claim(ctx, c.manifest.Run)
	if err != nil || recovered.ID != j.ID || recovered.Generation <= j.Generation || len(recovered.Samples) != 1 {
		t.Fatalf("recovery lost identity/evidence: %+v %v", recovered, err)
	}
	if _, err = s.observe(ctx, j.ID, j.Generation, c.view); !errors.Is(err, errFenced) {
		t.Fatal("old generation accepted", err)
	}
	if _, err = other.db.Exec(`UPDATE investigations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err = other.maintain(ctx, c.manifest.Run); err != nil {
		t.Fatal(err)
	}
	if got = apiTask(t, b, j.ID); got.State != "failed" || got.Reclaims != 1 {
		t.Fatalf("unbounded reclaim: %+v", got)
	}
	for _, state := range []string{"expired", "blocked"} {
		id := randomID(16)
		if w := a.submit(id, "b"); w.Code != 202 {
			t.Fatal(w.Body.String())
		}
		run := c.manifest.Run
		if state == "expired" {
			if _, err = s.db.Exec(`UPDATE investigations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
		} else {
			run = "new-run"
		}
		if err = s.maintain(ctx, run); err != nil {
			t.Fatal(err)
		}
		if got = apiTask(t, a, id); got.State != state {
			t.Fatalf("want %s, got %s", state, got.State)
		}
	}
}

func TestInvestigationRetriesTransientObservationErrors(t *testing.T) {
	for _, sample := range []int{1, 3} {
		t.Run(fmt.Sprintf("sample-%d", sample), func(t *testing.T) {
			s, _ := testTasks(t)
			// Sequence advancement survives transaction rollback, so exactly one
			// observation write fails. The third sample also tests failure after
			// observe has calculated its completion result but before commit.
			_, err := s.db.Exec(fmt.Sprintf(`
CREATE SEQUENCE observation_failure;
CREATE FUNCTION fail_observation_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF jsonb_array_length(NEW.samples) = %d AND jsonb_array_length(OLD.samples) < %d THEN
  IF nextval('observation_failure') = 1 THEN
   RAISE EXCEPTION 'injected transient observation failure' USING ERRCODE = '40001';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER observation_failure BEFORE UPDATE OF samples ON investigations
 FOR EACH ROW EXECUTE FUNCTION fail_observation_once();`, sample, sample))
			if err != nil {
				t.Fatal(err)
			}
			c, _ := fixture()
			a := taskAPI(t, c, s)
			id := randomID(16)
			if w := a.submit(id, "a"); w.Code != 202 {
				t.Fatal(w.Body.String())
			}
			ctx, cancel := context.WithCancel(context.Background())
			stopped := make(chan struct{})
			go func() { defer close(stopped); s.work(ctx, c) }()
			t.Cleanup(func() { cancel(); <-stopped })
			var j investigation
			for deadline := time.Now().Add(18 * time.Second); time.Now().Before(deadline); {
				j = apiTask(t, a, id)
				if j.State == "completed" || j.Reclaims != 0 {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if j.State != "completed" || j.Reclaims != 0 || len(j.Samples) != 3 || j.Result == nil {
				t.Fatalf("transient error abandoned the investigation: state=%s reclaims=%d samples=%d", j.State, j.Reclaims, len(j.Samples))
			}
			var attempts int
			if err = s.db.QueryRow(`SELECT last_value FROM observation_failure`).Scan(&attempts); err != nil || attempts != 2 {
				t.Fatalf("observation fault/retry not exercised: attempts=%d error=%v", attempts, err)
			}
		})
	}
}

func TestInvestigationWorkersAndEventResume(t *testing.T) {
	s, dsn := testTasks(t)
	c, _ := fixture()
	a := taskAPI(t, c, s)
	ids := []string{randomID(16), randomID(16)}
	for i, id := range ids {
		if w := a.submit(id, []string{"a", "b"}[i]); w.Code != 202 {
			t.Fatal(w.Body.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); s.work(ctx, c) }()
	}
	t.Cleanup(func() { cancel(); wg.Wait() })
	sawParallel := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		first, second := apiTask(t, a, ids[0]), apiTask(t, a, ids[1])
		if first.State == "running" && second.State == "running" {
			sawParallel = true
		}
		if first.State == "completed" && second.State == "completed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !sawParallel {
		t.Fatal("workers did not overlap")
	}
	for _, id := range ids {
		j := apiTask(t, a, id)
		if j.State != "completed" || len(j.Samples) != 3 || j.Result == nil || j.Result.Conclusion != "visibility_limited" {
			t.Fatalf("wrong durable result: %+v", j)
		}
		if j.Samples[2].At.Sub(j.Samples[0].At) < 10*time.Second {
			t.Fatal("observation window fabricated")
		}
		for _, sample := range j.Samples {
			for _, n := range sample.Evidence {
				if n.Chain != j.Chain {
					t.Fatal("cross-chain evidence")
				}
			}
		}
	}
	cancel()
	wg.Wait()
	s.db.Close()
	reopened, err := openTasks(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	a = taskAPI(t, c, reopened)
	if apiTask(t, a, ids[0]).State != "completed" {
		t.Fatal("service restart lost result")
	}
	events, err := reopened.events(context.Background(), 0)
	if err != nil || len(events) < 6 {
		t.Fatalf("events: %v %v", events, err)
	}
	cursor := events[len(events)-2].ID
	server := httptest.NewServer(a.h)
	defer server.Close()
	rctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	req, _ := http.NewRequestWithContext(rctx, "GET", server.URL+"/api/events", nil)
	req.AddCookie(a.cookie)
	req.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			var event taskEvent
			if err = json.Unmarshal(bytes.TrimPrefix(scanner.Bytes(), []byte("data: ")), &event); err != nil {
				t.Fatal(err)
			}
			if event.ID != events[len(events)-1].ID {
				t.Fatal("SSE replay skipped or repeated cursor", event.ID)
			}
			stop()
			return
		}
	}
	t.Fatal("SSE did not replay durable event", scanner.Err())
}
