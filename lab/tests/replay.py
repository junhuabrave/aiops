#!/usr/bin/env python3
"""Operator-side capture/demo, never a worker tool. No fault injection or models."""

import argparse
import json
import subprocess
import sys
import uuid

from smoke import ROOT, request, wait_for


def run(chain):
    request("/api/login", {"token": (ROOT / ".run/current/operator.token").read_text()})
    task_id = uuid.uuid4().hex
    body = {
        "id": task_id,
        "chain": chain,
        "question": "Capture observations for the offline agent-contract replay",
    }
    request("/api/investigations", body)
    print("Admitted diagnostic task", task_id, flush=True)

    def complete():
        task = request("/api/investigations/" + task_id)
        if task["state"] in ("failed", "blocked", "cancelled", "expired"):
            raise RuntimeError("Capture task ended: " + task["state"])
        return task["state"] == "completed"

    wait_for(complete, timeout=300)
    exported = request(f"/api/investigations/{task_id}/evidence")
    directory = ROOT / ".run/current" / ("replay-" + task_id)
    directory.mkdir(mode=0o700)
    evidence = directory / "evidence.json"
    evidence.write_text(json.dumps(exported))
    evidence.chmod(0o600)
    command = [sys.executable, str(ROOT / "ai/investigator.py"), str(evidence), "--demo"]
    for name, extra in (("full", []), ("restricted", ["--scope", chain + "-sub-1"])):
        result = subprocess.run(command + extra, check=True, capture_output=True, text=True)
        report = json.loads(result.stdout)
        assert report["status"] == "completed" and report["mode"] == "replay"
        assert report["usage"]["provider_calls"] == 0 and report["causality"] == "not_evaluated"
        allowed = (
            {chain + "-sub-1"}
            if name == "restricted"
            else {chain + "-" + suffix for suffix in ("publisher", "relay", "sub-1", "sub-2")}
        )
        assert all(c["resource"] in allowed for c in report["facts"])
        output = directory / (name + ".json")
        output.write_text(result.stdout)
        output.chmod(0o600)
        print(
            f"PASS {name}: {len(report['facts'])} validated observations; {report['usage']['replay_calls']} replay calls; no provider calls",
            flush=True,
        )
    # Admit both scopes before waiting: two independently isolated task slots.
    runs = []
    for profile in ("chain", "subscriber-1"):
        run_id = uuid.uuid4().hex
        request("/api/sandbox-investigations", {
            "id": run_id, "source_id": task_id, "profile": profile,
        })
        runs.append((profile, run_id))
    source_points = {p["id"]: p for p in json.loads(exported["payload"])["points"]}
    for profile, run_id in runs:
        def sandbox_complete():
            task = request("/api/sandbox-investigations/" + run_id)
            if task["state"] in ("blocked", "cancelled", "expired"):
                raise RuntimeError("Isolated replay ended: " + task["state"])
            return task if task["state"] == "completed" else None

        task = wait_for(sandbox_complete, timeout=100)
        result = task["result"]
        assert result["mode"] == "isolated_scripted_replay" and not result["live_model"]
        assert result["evidence_sha256"] == exported["sha256"]
        assert 0 < task["calls"] <= 12 and 0 < task["reserved_bytes"] <= 512 * 1024
        assert set(result["observations"]) == {"operations", "integration", "engineering"}
        assert result["observations"]["integration"] == []
        for points in result["observations"].values():
            for point in points:
                assert point == source_points[point["id"]] and point["availability"] == "fresh"
                assert point["resource"] in task["scope"]
                if profile == "subscriber-1":
                    assert point["resource"] == chain + "-sub-1"
        output = directory / ("sandbox-" + profile + ".json")
        output.write_text(json.dumps(task, indent=2))
        output.chmod(0o600)
        print(f"PASS isolated {profile}: three sandboxed specialists; durable budget {task['reserved_bytes']} bytes reserved; no provider calls", flush=True)
    print("Replay evidence and reports:", directory, flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--chain", choices=["a", "b"], default="a")
    run(parser.parse_args().chain)
