package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
)

func decodeExact(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

var sandboxTools = map[string][]string{
	"operations":  {"topology_slice", "delivery_window", "queue_window", "telemetry_health", "runbook_lookup"},
	"integration": {"topology_slice", "recent_changes", "delivery_window", "runbook_lookup"},
	"engineering": {"topology_slice", "resource_window", "queue_window", "telemetry_health", "runbook_lookup"},
}

type workerRequest struct {
	Kind        string   `json:"kind"`
	Tool        string   `json:"tool,omitempty"`
	Resources   []string `json:"resources,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type workerReply struct {
	Tool      string             `json:"tool"`
	Status    string             `json:"status"`
	Points    []evidencePoint    `json:"points,omitempty"`
	Resources []evidenceResource `json:"resources,omitempty"`
	Health    []map[string]any   `json:"health,omitempty"`
}

// role and scope are supplied by the broker's private pipe binding, never from
// the worker message. No generic query, URL, file read or provider tool exists.
func sandboxTool(w frozenWindow, role string, scope []string, call workerRequest) (out workerReply, err error) {
	if call.Kind != "tool" || len(call.EvidenceIDs) != 0 || !slices.Contains(sandboxTools[role], call.Tool) || len(call.Resources) > 4 {
		return out, errors.New("tool denied")
	}
	ids := call.Resources
	if len(ids) == 0 {
		ids = scope
	}
	for _, id := range ids {
		if !slices.Contains(scope, id) {
			return out, errors.New("resource denied")
		}
	}
	out.Tool, out.Status = call.Tool, "sampled_only"
	switch call.Tool {
	case "recent_changes", "runbook_lookup":
		out.Status = "unavailable"
	case "topology_slice":
		for _, r := range w.Resources {
			if slices.Contains(ids, r.ID) {
				out.Resources = append(out.Resources, r)
			}
		}
	default:
		for _, p := range w.Points {
			if !slices.Contains(ids, p.Resource) || (p.Peer != "" && !slices.Contains(ids, p.Peer)) {
				continue
			}
			// A relay total reveals the omitted subscriber by subtraction.
			if p.Resource == w.Chain+"-relay" && (p.Metric == "ingress" || p.Metric == "egress") &&
				(!slices.Contains(scope, w.Chain+"-sub-1") || !slices.Contains(scope, w.Chain+"-sub-2")) {
				continue
			}
			group := "delivery_window"
			switch p.Metric {
			case "queue_depth", "queue_oldest_seconds":
				group = "queue_window"
			case "cpu_seconds", "rss_bytes", "file_descriptors":
				group = "resource_window"
			}
			if call.Tool == "telemetry_health" {
				out.Health = append(out.Health, map[string]any{"id": p.ID, "resource": p.Resource, "metric": p.Metric,
					"observed_at": p.Observed, "collected_at": p.Collected, "availability": p.Availability})
			} else if call.Tool == group {
				out.Points = append(out.Points, p)
			}
		}
	}
	return
}

func sandboxSelection(call workerRequest, delivered map[string]evidencePoint) ([]evidencePoint, error) {
	if call.Kind != "finish" || call.Tool != "" || len(call.Resources) != 0 || len(call.EvidenceIDs) > 20 {
		return nil, errors.New("selection denied")
	}
	points := []evidencePoint{}
	seen := map[string]bool{}
	for _, id := range call.EvidenceIDs {
		p, ok := delivered[id]
		if !ok || p.Availability != "fresh" || seen[id] {
			return nil, errors.New("citation denied")
		}
		seen[id] = true
		// The worker selects citations only. Values and all other provenance are
		// copied from Go's delivered point, never from worker factual prose.
		points = append(points, p)
	}
	return points, nil
}
