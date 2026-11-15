package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func frozenFixture() investigation {
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	j := investigation{ID: strings.Repeat("a", 32), Run: "run", Chain: "a", State: "completed", Question: "Check delivery"}
	for i := range 3 {
		at := start.Add(time.Duration(i*5) * time.Second)
		d := diagnosis{At: at, Chain: "a", Summary: "BASELINE-ANSWER-MUST-NOT-LEAK", Conclusion: "supported"}
		for _, name := range []string{"publisher", "relay", "sub-1", "sub-2"} {
			role := name
			if strings.HasPrefix(name, "sub-") {
				role = "subscriber"
			}
			n := nodeView{ID: "a-" + name, Chain: "a", Role: role, Generation: "gen-" + name,
				Observed: at.Add(-time.Second), ResourceObserved: at.Add(-16 * time.Second),
				Delivery: &delivery{Ingress: 9007199254740993, Pending: 17}, Resources: &resources{RSSBytes: 8192}}
			if role == "relay" {
				n.Delivery.Queues = []queueStats{{Subscriber: "a-sub-1", Depth: 23, OldestSeconds: 2.75}, {Subscriber: "a-sub-2", Depth: 1, OldestSeconds: .125}}
			}
			if name == "sub-2" {
				n.Delivery = nil
			}
			d.Evidence = append(d.Evidence, n)
		}
		j.Samples = append(j.Samples, d)
	}
	return j
}

func TestFrozenEvidenceHTTP(t *testing.T) {
	s, _ := testTasks(t)
	c, _ := fixture()
	a := taskAPI(t, c, s)
	j := frozenFixture()
	path := "/api/investigations/" + j.ID + "/evidence"
	if w := (testAPI{h: a.h}).request("GET", path, ""); w.Code != 401 {
		t.Fatal("unprotected export", w.Code)
	}
	if w := a.request("GET", path, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := a.submit(j.ID, "a"); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if w := a.request("GET", path, ""); w.Code != 409 {
		t.Fatal("exported unfinished task", w.Code)
	}
	samples, _ := json.Marshal(j.Samples)
	if _, err := s.db.ExecContext(context.Background(), `UPDATE investigations SET state='completed',samples=$2 WHERE id=$1`, j.ID, samples); err != nil {
		t.Fatal(err)
	}
	w := a.request("GET", path, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var exported evidenceExport
	if err := json.Unmarshal(w.Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if exported.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(exported.Payload))) {
		t.Fatal("checksum mismatch")
	}
	if strings.Contains(exported.Payload, "BASELINE-ANSWER") || strings.Contains(exported.Payload, "rate_per_second") {
		t.Fatal("diagnosis or unqualified rate leaked")
	}
	if !strings.Contains(exported.Payload, `"value":9007199254740993`) {
		t.Fatal("large counter lost precision")
	}
	var window frozenWindow
	if err := json.Unmarshal([]byte(exported.Payload), &window); err != nil {
		t.Fatal(err)
	}
	if len(window.Points) != 120 || len(window.Resources) != 4 {
		t.Fatal("incomplete export")
	}
	for _, p := range window.Points {
		if strings.HasPrefix(p.Resource, "b-") {
			t.Fatal("cross-chain point")
		}
		switch {
		case p.Metric == "rss_bytes":
			if p.Availability != "stale" || p.Value != float64(8192) {
				t.Fatal("resource freshness lost", p)
			}
		case p.Resource == "a-sub-2" && p.Metric == "ingress":
			if p.Availability != "missing" || p.Value != nil || p.Observed != nil {
				t.Fatal("missing became zero", p)
			}
		case p.Metric == "queue_oldest_seconds" && p.Peer == "a-sub-1":
			if p.Value != 2.75 || p.Availability != "fresh" {
				t.Fatal("wrong queue", p)
			}
		}
	}
	// Identical reads and later cancellation of a completed task cannot rewrite it.
	a.request("POST", "/api/investigations/"+j.ID+"/cancel", "{}")
	if again := a.request("GET", path, ""); again.Body.String() != w.Body.String() {
		t.Fatal("frozen export changed")
	}
	for _, mutate := range []func(*investigation){
		func(j *investigation) { j.Samples[0].Evidence[0].Generation = "" },
		func(j *investigation) { j.Samples[2].Evidence[0].Generation = "replacement" },
		func(j *investigation) { j.Samples[1].Evidence[0].Chain = "b" },
		func(j *investigation) { j.Samples[1].At = j.Samples[0].At },
	} {
		invalid := frozenFixture()
		mutate(&invalid)
		if _, err := exportEvidence(invalid); err == nil {
			t.Fatal("exported inconsistent provenance")
		}
	}
}
