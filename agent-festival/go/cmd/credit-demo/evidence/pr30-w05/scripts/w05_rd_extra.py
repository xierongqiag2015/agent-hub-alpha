#!/usr/bin/env python3
"""W05 extra recovery scenarios for the PR #30 matrix (R-D), on top of the
repository's smoke_bus.py helpers. Run from deploy/ with the fault-proxy
overlay up (deploy/README.md "故障脚本"), so every SendMessage is counted
at the transport by its JSON-RPC id (= request_id).

  go-kill      SIGKILL the Go control service while a completed node and
               in-flight remote nodes exist, restart it, observe takeover.
  two-clients  Put a run into waiting (one lost GetTask reply), then resume
               it from two clients at the same instant: the plugin API
               (/api/resume) and a signed-in confirm-page session.

The script records what happened and evaluates checks; it does not stop at
the first unexpected result, so a deviation is kept as evidence. Evidence
goes to $DEMO_CACHE/evidence/agent-bus/<scenario>-<ts>.json.
"""
from __future__ import annotations
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.getcwd())
import smoke_bus as sb  # noqa: E402  (deploy/smoke_bus.py of the same checkout)

PASSWORD = os.environ.get("CREDIT_DEMO_PASSWORD", "demo")


def transport_sends(request_ids, task_ids=()):
    """SendMessage count per request id; events also include GetTask on the run's remote tasks."""
    lines = sb.docker("fault-proxy", "exec", "cat", "/tmp/bus-fault-events.jsonl").splitlines()
    events = [json.loads(line) for line in lines if line.strip()]
    mine = [e for e in events if e.get("id") in request_ids or (e.get("task_id") and e.get("task_id") in task_ids)]
    sends = {}
    for e in mine:
        if e["method"] == "SendMessage":
            sends[e["id"]] = sends.get(e["id"], 0) + 1
    return sends, mine


def wait_healthy(service, seconds=120):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        state = sb.docker(service, "inspect", "--format", "{{.State.Health.Status}}").strip()
        if state == "healthy":
            return True
        time.sleep(1)
    return False


def go_kill(evidence):
    run_id, plan_id = sb.new_run("go-kill")
    evidence.update(run_id=run_id, plan_id=plan_id)
    state = {"injected": False}

    def hook(graph):
        if state["injected"]:
            return
        nodes = {n["id"]: n for n in graph["nodes"]}
        inflight = [k for k in ("financial", "industry", "bureau")
                    if nodes[k].get("remote", {}).get("task_id") and nodes[k].get("status") != "done"]
        if nodes["intake"]["status"] == "done" and inflight:
            run = sb.read_run(run_id)
            evidence["before_kill"] = sb.call_snapshot(run)
            evidence["before_kill_steps"] = {k: s["status"] for k, s in run["steps"].items()}
            evidence["before_kill_leases"] = len(run.get("leases", []))
            evidence["killed_at"] = time.time()
            sb.docker("credit", "kill", "--signal", "KILL")
            time.sleep(5)
            sb.docker("credit", "start")
            evidence["credit_healthy_again"] = wait_healthy("credit")
            evidence["restarted_at"] = time.time()
            state["injected"] = True

    graph = sb.wait_run(run_id, evidence, hook, timeout=600)
    run = sb.read_run(run_id)
    evidence["run"] = sb.redact(run)
    before = evidence.get("before_kill", {})
    after = sb.call_snapshot(run)
    sends, events = transport_sends({c["request_id"] for c in after.values()}, {c["remote_task_id"] for c in after.values() if c["remote_task_id"]})
    evidence["transport_events"] = events
    completed_before = [k for k, s in evidence.get("before_kill_steps", {}).items() if s == "done" and k in before]
    evidence["checks"] = {
        "fault_injected": state["injected"],
        "final_status": graph["run"]["status"],
        "completed": graph["run"]["status"] == "done",
        "taken_over_by_new_lease": len(run.get("leases", [])) > evidence.get("before_kill_leases", 0),
        "completed_nodes_unchanged": all(after[k][f] == before[k][f] for k in completed_before
                                         for f in ("request_id", "artifact_id", "artifact_digest", "model_events")),
        "inflight_kept_request_and_task": all(after[k]["request_id"] == before[k]["request_id"] and
                                              (not before[k]["remote_task_id"] or after[k]["remote_task_id"] == before[k]["remote_task_id"])
                                              for k in before if k not in completed_before),
        "one_send_per_request": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
        "completed_before_kill": completed_before,
    }


def ui_session():
    cookies = http.cookiejar.CookieJar()

    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None

    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies), NoRedirect)
    payload = urllib.parse.urlencode({"user": "alice", "password": PASSWORD, "next": "/ui/"}).encode()
    try:
        opener.open(sb.BASE + "/ui/login", payload, timeout=10).close()
    except urllib.error.HTTPError as exc:
        if exc.code not in (302, 303, 404):
            raise
    assert any(c.name == "cd_session" for c in cookies), "Login did not issue a session"
    return opener


def two_clients(evidence):
    sb.docker("fault-proxy", "exec", "python", "-c", "from pathlib import Path; Path('/tmp/bus-fault-mode').write_text('drop-query-once')")
    run_id, plan_id = sb.new_run("two-clients")
    evidence.update(run_id=run_id, plan_id=plan_id)
    graph = sb.wait_run(run_id, evidence)
    evidence["status_before_resume"] = graph["run"]["status"]
    run = sb.read_run(run_id)
    before = sb.call_snapshot(run)
    evidence["before_resume"] = before
    evidence["before_resume_leases"] = len(run.get("leases", []))
    opener = ui_session()
    barrier = threading.Barrier(2)
    results = {}

    def plugin_api():
        barrier.wait()
        started = time.time()
        req = urllib.request.Request(sb.BASE + "/api/resume", data=json.dumps({"run": run_id, "user": "alice", "password": PASSWORD}).encode(),
                                     headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=15) as resp:
                results["plugin_api"] = {"http": resp.status, "body": resp.read().decode()[:200], "at": started}
        except urllib.error.HTTPError as exc:
            results["plugin_api"] = {"http": exc.code, "body": exc.read().decode()[:200], "at": started}

    def confirm_page():
        barrier.wait()
        started = time.time()
        try:
            with opener.open(sb.BASE + f"/ui/run/{run_id}/resume", b"", timeout=15) as resp:
                results["confirm_page"] = {"http": resp.status, "at": started}
        except urllib.error.HTTPError as exc:
            results["confirm_page"] = {"http": exc.code, "body": exc.read().decode()[:200], "at": started}

    threads = [threading.Thread(target=plugin_api), threading.Thread(target=confirm_page)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    evidence["resume_results"] = results
    graph = sb.wait_run(run_id, evidence, timeout=600)
    run = sb.read_run(run_id)
    evidence["run"] = sb.redact(run)
    after = sb.call_snapshot(run)
    sends, events = transport_sends({c["request_id"] for c in after.values()}, {c["remote_task_id"] for c in after.values() if c["remote_task_id"]})
    evidence["transport_events"] = events
    accepted = [k for k, v in results.items() if v["http"] in (200, 303)]
    rejected = [k for k, v in results.items() if v["http"] == 409]
    evidence["checks"] = {
        "was_waiting": evidence["status_before_resume"] == "waiting",
        "exactly_one_accepted": len(accepted) == 1 and len(rejected) == 1,
        "accepted": accepted,
        "rejected": rejected,
        "one_new_lease": len(run.get("leases", [])) == evidence["before_resume_leases"] + 1,
        "kept_request_and_task": all(after[k]["request_id"] == before[k]["request_id"] and
                                     (not before[k]["remote_task_id"] or after[k]["remote_task_id"] == before[k]["remote_task_id"])
                                     for k in before),
        "one_send_per_request": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
        "final_status": graph["run"]["status"],
        "completed": graph["run"]["status"] == "done",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("scenario", choices=["go-kill", "two-clients"])
    args = parser.parse_args()
    evidence = {"scenario": args.scenario, "started_at": time.time(), "script": "evidence/pr30-w05/scripts/w05_rd_extra.py",
                "scope": "W05 single-side R-D extras on synthetic credit data", "snapshots": [], "environment": sb.environment_record()}
    directory = sb.CACHE / "evidence" / "agent-bus"
    directory.mkdir(parents=True, exist_ok=True)
    target = directory / f"{args.scenario}-{int(time.time())}.json"
    try:
        {"go-kill": go_kill, "two-clients": two_clients}[args.scenario](evidence)
    finally:
        evidence["finished_at"] = time.time()
        target.write_text(json.dumps(sb.redact(evidence), ensure_ascii=False, indent=2) + "\n")
        print("Evidence:", target)
        print("checks:", json.dumps(evidence.get("checks"), ensure_ascii=False))


if __name__ == "__main__":
    main()
