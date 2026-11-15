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
    print("Replay evidence and reports:", directory, flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--chain", choices=["a", "b"], default="a")
    run(parser.parse_args().chain)
