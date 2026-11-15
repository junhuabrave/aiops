package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errEvidenceUnavailable = errors.New("Frozen evidence requires a completed three-snapshot investigation with recorded process generations")

// This is an offline export, not a worker capability or an authorization token.
// Hash the exact UTF-8 payload string so another language need not reproduce Go's
// JSON number/escape formatting. A checksum detects damage, not forgery.
type evidenceExport struct {
	Payload string `json:"payload"`
	SHA256  string `json:"sha256"`
}

type evidencePoint struct {
	ID           string     `json:"id"`
	Resource     string     `json:"resource"`
	Generation   string     `json:"generation"`
	Peer         string     `json:"peer"`
	Metric       string     `json:"metric"`
	Unit         string     `json:"unit"`
	Kind         string     `json:"kind"`
	Observed     *time.Time `json:"observed_at"`
	Collected    time.Time  `json:"collected_at"`
	Availability string     `json:"availability"`
	Value        any        `json:"value"`
}

type evidenceResource struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	Generation string `json:"generation"`
}

type frozenWindow struct {
	Version   string             `json:"version"`
	Task      string             `json:"task_id"`
	Run       string             `json:"run_id"`
	Chain     string             `json:"chain"`
	Question  string             `json:"question"`
	Start     time.Time          `json:"start"`
	End       time.Time          `json:"end"`
	Resources []evidenceResource `json:"resources"`
	Points    []evidencePoint    `json:"points"`
	Limits    []string           `json:"limitations"`
}

func exportEvidence(j investigation) (evidenceExport, error) {
	if j.State != "completed" || len(j.Samples) != 3 || (j.Chain != "a" && j.Chain != "b") {
		return evidenceExport{}, errEvidenceUnavailable
	}
	w := frozenWindow{Version: "frozen-window-v1", Task: j.ID, Run: j.Run, Chain: j.Chain, Question: j.Question,
		Start: j.Samples[0].At, End: j.Samples[2].At,
		Limits: []string{"Three sampled snapshots, not continuous history or current plant state.",
			"Only the selected chain is exported; no cross-chain control or host-wide evidence.",
			"Changes and independent session calendar are unavailable. Causality is not established.",
			"Derived rates are excluded: gap-aware rate qualification is not implemented."}}
	if w.End.Sub(w.Start) > 10*time.Minute || !w.End.After(w.Start) {
		return evidenceExport{}, errEvidenceUnavailable
	}
	identities := map[string]evidenceResource{}
	for i, sample := range j.Samples {
		if sample.Chain != j.Chain || len(sample.Evidence) != 4 || (i > 0 && !sample.At.After(j.Samples[i-1].At)) {
			return evidenceExport{}, errEvidenceUnavailable
		}
		seen := map[string]bool{}
		for _, n := range sample.Evidence {
			roles := map[string]string{j.Chain + "-publisher": "publisher", j.Chain + "-relay": "relay", j.Chain + "-sub-1": "subscriber", j.Chain + "-sub-2": "subscriber"}
			if n.Chain != j.Chain || n.Generation == "" || roles[n.ID] != n.Role || n.Role == "" || seen[n.ID] {
				return evidenceExport{}, errEvidenceUnavailable
			}
			seen[n.ID] = true
			identity := evidenceResource{n.ID, n.Role, n.Generation}
			if i == 0 {
				identities[n.ID] = identity
				w.Resources = append(w.Resources, identity)
			} else if identities[n.ID] != identity {
				return evidenceExport{}, errEvidenceUnavailable
			}
			add := func(metric, unit, kind, peer string, value any, observed time.Time, available bool, maxAge time.Duration) {
				p := evidencePoint{ID: fmt.Sprintf("%d/%s/%s/%s", i, n.ID, metric, peer), Resource: n.ID,
					Generation: n.Generation, Peer: peer, Metric: metric, Unit: unit, Kind: kind,
					Collected: sample.At, Availability: "missing"}
				if available && !observed.IsZero() {
					p.Observed, p.Value, p.Availability = &observed, value, "fresh"
					age := sample.At.Sub(observed)
					if age < -2*time.Second || age > maxAge {
						p.Availability = "stale"
					}
				}
				w.Points = append(w.Points, p)
			}
			d := delivery{}
			if n.Delivery != nil {
				d = *n.Delivery
			}
			for _, m := range []struct {
				name, unit, kind string
				value            any
			}{
				{"ingress", "messages", "counter", d.Ingress}, {"egress", "messages", "counter", d.Egress},
				{"consumed", "messages", "counter", d.Consumed}, {"pending", "messages", "gauge", d.Pending},
				{"pending_age_seconds", "seconds", "gauge", d.PendingAge},
				{"last_receive_age_seconds", "seconds", "gauge", d.LastReceiveAge},
			} {
				add(m.name, m.unit, m.kind, "", m.value, n.Observed, n.Delivery != nil, 3*time.Second)
			}
			if n.Role == "relay" {
				for _, peer := range []string{j.Chain + "-sub-1", j.Chain + "-sub-2"} {
					q, found := queueStats{}, false
					for _, queue := range d.Queues {
						if queue.Subscriber == peer {
							q, found = queue, true
						}
					}
					add("queue_depth", "messages", "gauge", peer, q.Depth, n.Observed, found, 3*time.Second)
					add("queue_oldest_seconds", "seconds", "gauge", peer, q.OldestSeconds, n.Observed, found, 3*time.Second)
				}
			}
			r := resources{}
			if n.Resources != nil {
				r = *n.Resources
			}
			for _, m := range []struct {
				name, unit, kind string
				value            any
			}{
				{"cpu_seconds", "seconds", "counter", r.CPUSeconds}, {"rss_bytes", "bytes", "gauge", r.RSSBytes},
				{"file_descriptors", "descriptors", "gauge", r.FDs},
			} {
				add(m.name, m.unit, m.kind, "", m.value, n.ResourceObserved, n.Resources != nil, 15*time.Second)
			}
		}
	}
	b, err := json.Marshal(w)
	if err != nil {
		return evidenceExport{}, err
	}
	if len(b) > 128<<10 {
		return evidenceExport{}, errEvidenceUnavailable
	}
	return evidenceExport{string(b), fmt.Sprintf("%x", sha256.Sum256(b))}, nil
}
