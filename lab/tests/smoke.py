#!/usr/bin/env python3
"""Offline functional checks against a disposable lab; never calls a model.

This is an operator/evaluator script. It must not be mounted in future workers.
"""
import argparse
import datetime as dt
import http.cookiejar
import json
import pathlib
import socket
import subprocess
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
BASE = "http://127.0.0.1:8080"
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def request(path, body=None):
    headers = {"Origin": BASE, "Content-Type": "application/json"}
    data = None if body is None else json.dumps(body).encode()
    with client.open(urllib.request.Request(BASE + path, data=data, headers=headers), timeout=5) as response:
        return json.load(response)


def scenario(name):
    subprocess.run([str(ROOT / "bin/plantlab"), "scenario", "-case", name], cwd=ROOT, check=True)


def wait_for(check, timeout=30):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        result = check()
        if result:
            return result
        time.sleep(.5)
    raise AssertionError("Timed out waiting for observed condition")


def nodes():
    return {n["id"]: n for n in request("/api/state")["nodes"]}


def alerts():
    return request("/api/alerts")["data"]["alerts"]


def healthy():
    n = nodes()
    return len(n) == 8 and all(x["state"] == "healthy" and x["rate_per_second"] is not None and x["rate_per_second"] > 450 for x in n.values())


def journal_alert(name, since):
    path = ROOT / ".run/current/alerts.jsonl"
    if not path.exists():
        return None
    for line in path.read_text().splitlines():
        event = json.loads(line)
        at = dt.datetime.fromisoformat(event["received_at"].replace("Z", "+00:00")).timestamp()
        if at < since:
            continue
        for alert in event["notification"].get("alerts", []):
            if alert["labels"]["alertname"] == name and alert["status"] == "firing":
                return at
    return None


def run(args):
    token = (ROOT / ".run/current/operator.token").read_text()
    request("/api/login", {"token": token})
    report = {"run_id": request("/api/state")["run_id"], "mode": "no-model", "healthy_seconds": args.healthy_seconds, "warmup_seconds": args.warmup, "results": []}
    scenario("healthy")
    wait_for(healthy)
    time.sleep(args.warmup)
    start = time.time()
    baseline = nodes()
    deadline = time.monotonic() + args.healthy_seconds
    while time.monotonic() < deadline:
        assert healthy(), "Healthy delivery or fresh telemetry lost"
        assert not alerts(), "Healthy baseline raised an alert"
        time.sleep(2)
    finish = nodes()
    for name, n in finish.items():
        for field in ["missing", "duplicates", "out_of_order", "corrupt"]:
            assert n["delivery"][field] == baseline[name]["delivery"][field], (name, field)
    # Pause publication and drain before independently reconciling sequence receipts.
    scenario("quiet-session")
    wait_for(lambda: all(x["rate_per_second"] == 0 for x in nodes().values()))
    ledgers = {}
    for role in ["publisher", "sub-1", "sub-2"]:
        for chain in ["a", "b"]:
            rows = []
            for line in (ROOT / f".run/current/{chain}-{role}.ledger.jsonl").open():
                row = json.loads(line)
                if row["event"] in ["published", "received"]:
                    at = dt.datetime.fromisoformat(row["at"].replace("Z", "+00:00")).timestamp()
                    if at >= start:
                        rows.append(row["sequence"])
            ledgers[chain + "-" + role] = rows
    for chain in ["a", "b"]:
        sent = ledgers[chain + "-publisher"]
        assert sent and all(b == a + 1 for a, b in zip(sent, sent[1:])), "Publisher ledger not contiguous"
        for sub in ["sub-1", "sub-2"]:
            # A frame published before the time boundary can arrive after it.
            received = [s for s in ledgers[chain + "-" + sub] if s >= sent[0]]
            assert received == sent, (chain, sub, "independent ledger mismatch")
    report["results"].append({"case": "healthy", "pass": True, "reconciled_publications": sum(len(ledgers[c + "-publisher"]) for c in ["a", "b"])})
    print("PASS healthy delivery, no sequence anomalies, independent ledgers reconciled", flush=True)
    for case, alert_name, expected in [
        ("slow-consumer", "QueueDelayed", "slow-consumption hypothesis"),
        ("relay-bottleneck", "QueueDelayed", "without a matching receiver-consumption backlog"),
        ("upstream-interruption", "DeliverySilent", "upstream interruption"),
        ("telemetry-failure", "TelemetryMissing", "No root cause is established"),
    ]:
        scenario("healthy")
        wait_for(healthy)
        wait_for(lambda: not alerts())
        control = nodes()
        since = time.time()
        scenario(case)
        # Record first observed threshold breach separately from injection and journal receipt.
        def threshold():
            n = nodes()
            if case in ["slow-consumer", "relay-bottleneck"]:
                return any(q["oldest_seconds"] > 2 for q in n["a-relay"]["delivery"]["queues"])
            if case == "upstream-interruption":
                return n["a-sub-1"]["delivery"]["last_receive_age_seconds"] > 3
            return n["a-relay"]["observation_age_seconds"] > 3
        wait_for(threshold)
        breach = time.time()
        receipt = wait_for(lambda: journal_alert(alert_name, since), timeout=15)
        assert receipt - breach <= 15, "Alert journal gate exceeded"
        d = request("/api/investigate", {"chain": "a", "question": "What does the current delivery evidence support?"})
        assert expected in d["summary"], d
        current = nodes()
        for name in ["b-publisher", "b-relay", "b-sub-1", "b-sub-2"] + (["a-sub-2"] if case == "slow-consumer" else []):
            assert current[name]["rate_per_second"] > 450, (case, name, "control slowed")
            assert current[name]["delivery"]["missing"] == control[name]["delivery"]["missing"], (case, name, "control lost data")
        if case == "telemetry-failure":
            assert current["a-sub-1"]["rate_per_second"] > 450, "Telemetry fault stopped delivery"
        result = {"case": case, "pass": True, "threshold_to_journal_seconds": round(receipt-breach,3), "conclusion": d["conclusion"]}
        report["results"].append(result)
        print("PASS", result, flush=True)
    for case in ["healthy-busy", "quiet-session", "quiet-telemetry-failure"]:
        scenario("healthy");wait_for(healthy);wait_for(lambda: not alerts())
        since = time.time();scenario(case)
        time.sleep(12)
        observed = alerts()
        if case == "quiet-telemetry-failure":
            assert journal_alert("TelemetryMissing", since), "Quiet mode hid visibility loss"
            assert not any(a["labels"]["alertname"] == "DeliverySilent" for a in observed)
        else:
            assert not observed, (case, observed)
            if case == "healthy-busy":
                assert all(x["rate_per_second"] > 900 for x in nodes().values()), "Busy control failed to reach offered load"
            else:
                assert all(x["rate_per_second"] == 0 for x in nodes().values()), "Quiet control still delivering"
        report["results"].append({"case": case, "pass": True})
        print("PASS", case, flush=True)
    scenario("healthy");wait_for(healthy)
    path = ROOT / ".run/current/verification.json"
    path.write_text(json.dumps(report, indent=2) + "\n")
    print("PASS all functional scenarios; report:", path, flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--existing", action="store_true", help="Use an already supervised disposable lab")
    parser.add_argument("--healthy-seconds", type=int, default=15)
    parser.add_argument("--warmup", type=int, default=5)
    args = parser.parse_args()
    process = None
    log = None
    try:
        if not args.existing:
            try:
                with socket.create_connection(("127.0.0.1", 8080), timeout=1):
                    pass
            except OSError:
                pass
            else:
                raise RuntimeError("Port 8080 is already in use; refusing to alter an existing run without --existing")
            (ROOT / ".run").mkdir(exist_ok=True)
            log = (ROOT / ".run/smoke-launch.log").open("w")
            process = subprocess.Popen([str(ROOT / "bin/plantlab"), "up"], cwd=ROOT, stdout=log, stderr=log)
            for _ in range(60):
                if process.poll() is not None:
                    raise RuntimeError("Lab exited; see .run/smoke-launch.log")
                try:
                    urllib.request.urlopen(BASE + "/healthz", timeout=1).close()
                    break
                except (OSError, urllib.error.URLError):
                    time.sleep(.5)
            else:
                raise RuntimeError("Lab did not become ready")
            if process.poll() is not None:
                raise RuntimeError("Lab exited before verification")
        run(args)
    finally:
        if process:
            process.terminate()
            process.wait(timeout=15)
        if log:
            log.close()
