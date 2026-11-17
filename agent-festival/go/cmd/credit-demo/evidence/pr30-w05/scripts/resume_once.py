#!/usr/bin/env python3
"""Resume a waiting run once through /api/resume and record where it ends.
Run from deploy/ (imports smoke_bus); appends to the given evidence file."""
import json, os, sys, time, urllib.error, urllib.request
sys.path.insert(0, os.getcwd())
import smoke_bus as sb

run_id, path = sys.argv[1], sys.argv[2]
ev = json.load(open(path, encoding="utf-8"))
before = sb.read_run(run_id)
req = urllib.request.Request(sb.BASE + "/api/resume", data=json.dumps({"run": run_id, "user": "alice", "password": os.environ.get("CREDIT_DEMO_PASSWORD", "demo")}).encode(),
                             headers={"Content-Type": "application/json"})
try:
    with urllib.request.urlopen(req, timeout=15) as resp:
        answer = {"http": resp.status, "body": resp.read().decode()[:200]}
except urllib.error.HTTPError as exc:
    answer = {"http": exc.code, "body": exc.read().decode()[:200]}
record = {"at": time.time(), "resume": answer, "status_before": before["status"], "snapshots": []}
graph = sb.wait_run(run_id, record, timeout=600) if answer["http"] == 200 else None
after = sb.read_run(run_id)
record["snapshots"] = record["snapshots"][-3:]
record["status_after"] = after["status"]
record["final_output"] = after.get("final_output")
record["steps_after"] = {k: s["status"] for k, s in after["steps"].items()}
record["calls_after"] = sb.call_snapshot(after)
record["leases_after"] = len(after.get("leases", []))
ev["manual_resume_after_go_kill"] = sb.redact(record)
json.dump(ev, open(path, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
print(json.dumps({k: record[k] for k in ("resume", "status_before", "status_after", "final_output", "steps_after", "leases_after")}, ensure_ascii=False))
