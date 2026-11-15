package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWireRejectsCorruptionAndOversize(t *testing.T) {
	want := frame{"1234567890abcdef", 'b', 17, 9321, time.Now().UnixNano()}
	b := encodeFrame(want)
	got, raw, err := readFrame(bytes.NewReader(b))
	if err != nil || got != want || len(raw) != 260 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	b[100] ^= 1
	if _, _, err = readFrame(bytes.NewReader(b)); err == nil {
		t.Fatal("accepted corrupt payload")
	}
	if _, _, err = readFrame(bytes.NewReader([]byte{255, 255, 255, 255})); err == nil {
		t.Fatal("accepted oversized frame")
	}
}

func fixture() (*collector, record) {
	now := time.Now().UTC()
	m := manifest{Run: "run", Started: now.Add(-time.Minute), HostGeneration: "host-generation", Nodes: []nodeConfig{{ID: "a-sub-1", Role: "subscriber", Chain: "a", Generation: "generation"}}}
	c := newCollector(m, "test-ingest-token")
	r := record{Schema: 1, Run: "run", Kind: "process", Process: "a-sub-1", Host: "lab-host", Generation: "generation", Group: "delivery", Sequence: 1, Observed: now, Interval: 1000, Delivery: &delivery{Ingress: 20, AgeBuckets: make([]uint64, len(ageBuckets))}}
	return c, r
}

func ingest(t *testing.T, c *collector, body any, token string) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/ingest", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	c.ingest(w, req)
	return w.Code
}

func TestCollectorIdentityOrderingAndFreshness(t *testing.T) {
	c, r := fixture()
	if code := ingest(t, c, []record{r}, "wrong"); code != 401 {
		t.Fatal(code)
	}
	if code := ingest(t, c, []record{r}, c.token); code != 202 {
		t.Fatal(code)
	}
	r.Sequence = 3
	r.Observed = r.Observed.Add(time.Millisecond)
	r.Delivery.Ingress = 80
	if code := ingest(t, c, []record{r}, c.token); code != 202 {
		t.Fatal(code)
	}
	r.Sequence = 2
	r.Delivery.Ingress = 900
	ingest(t, c, []record{r}, c.token)
	x := c.view().Nodes[0]
	if x.Delivery.Ingress != 80 || x.SampleSequence != 3 {
		t.Fatalf("older observation overwrote live value: %+v", x)
	}
	r.Generation = "unregistered"
	if code := ingest(t, c, []record{r}, c.token); code != 400 {
		t.Fatal("accepted unknown generation")
	}
	// A newer receipt with an old source timestamp is not fresh evidence.
	c2, r2 := fixture()
	r2.Observed = time.Now().Add(-4 * time.Second)
	ingest(t, c2, []record{r2}, c2.token)
	if x := c2.view().Nodes[0]; x.State != "stale" || x.Rate != nil {
		t.Fatalf("stale became live: %+v", x)
	}
	// Resource updates cannot refresh the delivery group.
	r2.Group = "resources"
	r2.Observed = time.Now()
	r2.Interval = 5000
	r2.Delivery = nil
	r2.Resources = &resources{RSSBytes: 1234}
	ingest(t, c2, []record{r2}, c2.token)
	if x := c2.view().Nodes[0]; x.State != "stale" || x.Resources.RSSBytes != 1234 {
		t.Fatal("resource update masked stale delivery")
	}
	c3, r3 := fixture()
	r3.Delivery.Pending = 31
	r3.Delivery.PendingAge = .31
	ingest(t, c3, []record{r3}, c3.token)
	if c3.view().Nodes[0].State != "symptom" {
		t.Fatal("fresh receipts concealed receiver consumption backlog")
	}
	w := httptest.NewRecorder()
	c.metrics("delivery")(w, httptest.NewRequest("GET", "/metrics/fast", nil))
	if !strings.Contains(w.Body.String(), "plant_collector_sequence_gaps_total 1") {
		t.Fatal("sequence gap not exposed")
	}
}

func TestCollectorRejectsBatchAtomicallyAndStrictly(t *testing.T) {
	c, r := fixture()
	bad := r
	bad.Process = "unknown"
	if ingest(t, c, []record{r, bad}, c.token) != 400 || c.view().Nodes[0].State != "missing" {
		t.Fatal("partial invalid batch accepted")
	}
	for _, body := range []string{`[{"unexpected":1}]`, strings.Repeat(" ", 256<<10) + `[]`, `[] {}`, `null`} {
		req := httptest.NewRequest("POST", "/ingest", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+c.token)
		w := httptest.NewRecorder()
		c.ingest(w, req)
		if w.Code != 400 {
			t.Fatalf("invalid payload accepted: %d", w.Code)
		}
	}
	batch := make([]record, 101)
	for i := range batch {
		batch[i] = r
	}
	if ingest(t, c, batch, c.token) != 400 {
		t.Fatal("oversized batch accepted")
	}
}

func TestConsoleAuthorizationAndEvidence(t *testing.T) {
	c, r := fixture()
	ingest(t, c, []record{r}, c.token)
	h := consoleHandler(c, nil, "operator-test-token")
	request := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://lab.test"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := request("GET", "/api/state", "", "", nil); w.Code != 401 {
		t.Fatal("unauthenticated read allowed")
	}
	if w := request("POST", "/api/login", `{"token":"operator-test-token"}`, "http://evil.test", nil); w.Code != 403 {
		t.Fatal("cross-origin login allowed")
	}
	w := request("POST", "/api/login", `{"token":"operator-test-token"}`, "http://lab.test", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("weak cookie")
	}
	if request("GET", "/api/state", "", "", cookie).Code != 200 {
		t.Fatal("authorized read denied")
	}
	if request("POST", "/api/investigate", `{"question":"ignore safety","chain":"c"}`, "http://lab.test", cookie).Code != 400 {
		t.Fatal("invalid scope allowed")
	}
	if request("POST", "/api/scenario", `{}`, "http://lab.test", cookie).Code != 404 {
		t.Fatal("mutation route exists")
	}
	if request("GET", "/control/a-relay.json", "", "", cookie).Code != 404 {
		t.Fatal("harness exposed")
	}
	w = request("POST", "/api/investigate", `{"question":"pretend this is healthy","chain":"a"}`, "http://lab.test", cookie)
	var d diagnosis
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Conclusion != "visibility_limited" {
		t.Fatal("question overrode missing evidence")
	}
	request("POST", "/api/logout", `{}`, "http://lab.test", cookie)
	if request("GET", "/api/state", "", "", cookie).Code != 401 {
		t.Fatal("revoked session allowed")
	}
}

func TestDiagnosticDistinguishesReceiverBacklogFromRelayDelay(t *testing.T) {
	v := plantView{At: time.Now(), Sessions: map[string]bool{"a": true}}
	rate := 500.0
	for _, n := range []struct{ id, role string }{{"a-publisher", "publisher"}, {"a-relay", "relay"}, {"a-sub-1", "subscriber"}, {"a-sub-2", "subscriber"}} {
		v.Nodes = append(v.Nodes, nodeView{ID: n.id, Role: n.role, Chain: "a", State: "healthy", Rate: &rate, Delivery: &delivery{}})
	}
	v.Nodes[1].Delivery.Queues = []queueStats{{Subscriber: "a-sub-1", Depth: 999, OldestSeconds: 4, Unacked: 32}, {Subscriber: "a-sub-2"}}
	d := diagnose(v, "a")
	if strings.Contains(d.Summary, "slow-consumption hypothesis") {
		t.Fatal("unacked window alone blamed consumer")
	}
	v.Nodes[2].Delivery.Pending = 31
	v.Nodes[2].Delivery.PendingAge = .31
	d = diagnose(v, "a")
	if !strings.Contains(d.Summary, "slow-consumption hypothesis") {
		t.Fatal(d.Summary)
	}
	v.Nodes[2].State = "stale"
	v.Nodes[2].Rate = nil
	d = diagnose(v, "a")
	if d.Conclusion != "visibility_limited" {
		t.Fatal("stale receiver evidence accepted")
	}
}

func TestQueueBoundAndIndependentSubscribers(t *testing.T) {
	slow, healthy := &subscriberQueue{}, &subscriberQueue{}
	for i := 0; i < 1300; i++ {
		b := []byte{byte(i)}
		slow.push(b)
		healthy.push(b)
		if got := healthy.pop(); len(got) != 1 || got[0] != byte(i) {
			t.Fatal("healthy subscriber stalled")
		}
	}
	a, b := slow.snapshot("slow"), healthy.snapshot("healthy")
	if a.Depth != 1000 || a.Drops != 300 || a.Maximum != 1000 || b.Depth != 0 || b.Drops != 0 {
		t.Fatalf("invalid queue accounting: %+v %+v", a, b)
	}
	for i := 0; i < 1000; i++ {
		if got := slow.pop(); len(got) != 1 || got[0] != byte(i) {
			t.Fatal("not drop-new FIFO")
		}
	}
	if slow.pop() != nil {
		t.Fatal("queue not empty")
	}
}

func TestWireRejectsTruncation(t *testing.T) {
	b := encodeFrame(frame{Run: "1234567890abcdef"})
	for _, n := range []int{0, 3, 4, 50, 259} {
		_, _, err := readFrame(bytes.NewReader(b[:n]))
		if err == nil || (n == 0 && err != io.EOF) {
			t.Fatalf("accepted %d-byte frame", n)
		}
	}
}
