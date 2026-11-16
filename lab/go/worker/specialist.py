"""Scripted specialist for the isolation demo. No model or local evidence store."""

import json
import sys


def send(message):
    print(json.dumps(message), flush=True)


assignment = json.loads(sys.stdin.readline())
tool = {
    "operations": "delivery_window",
    "integration": "recent_changes",
    "engineering": "resource_window",
}[assignment["role"]]
send({"kind": "tool", "tool": tool, "resources": []})
reply = json.loads(sys.stdin.readline())
points = [p for p in reply.get("points", []) if p["availability"] == "fresh"]
send({"kind": "finish", "evidence_ids": [p["id"] for p in points[-2:]]})
