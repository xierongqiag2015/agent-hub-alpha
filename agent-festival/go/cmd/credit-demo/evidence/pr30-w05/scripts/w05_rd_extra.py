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
  storage-failure-traced
               smoke_bus.py storage-failure unchanged, with timestamps of every
               injection/restore command and /healthz sampled every 0.25 s.
  ra-completed-then-unknown
               after two remote nodes are done, lose the next GetTask reply so a
               successor becomes UNKNOWN; resume (--resume api|external).
  ra-unknown-at-report
               same, but armed only after the four investigation nodes and the
               local black-box node are done, so the report node becomes UNKNOWN.
  rc-resume-storage-failure
               while a run waits, make the state write fail and resume: the
               resume must be refused (503); after storage repair the run is
               still waiting and a second resume completes it.
  rb-send-reply-lost
               forward the first SendMessage but lose its reply (needs
               compose.w05-send-test.yaml); the call stays UNKNOWN without a
               task id and is never resent.
  rb-resume-reply-lost
               send a resume and drop the connection before the reply; the
               resume is persisted and a repeated resume has no second effect.

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


def storage_failure_traced(evidence):
    """smoke_bus.py storage-failure unchanged, plus a raw timeline: every docker
    command it issues (injection, release, restore, restarts) with timestamps,
    and /healthz polled every 0.25 s with status and body."""
    timeline, health = [], []
    original = sb.docker

    def traced(service, *args):
        started = time.time()
        try:
            out = original(service, *args)
            timeline.append({"at": started, "done": time.time(), "service": service, "command": list(args)[:2], "ok": True})
            return out
        except Exception as exc:
            timeline.append({"at": started, "done": time.time(), "service": service, "command": list(args)[:2], "ok": False, "error": str(exc)[:200]})
            raise

    stop = threading.Event()

    def poll():
        while not stop.is_set():
            at = time.time()
            try:
                with urllib.request.urlopen(sb.BASE + "/healthz", timeout=2) as resp:
                    health.append({"at": at, "http": resp.status, "body": resp.read().decode()[:120]})
            except urllib.error.HTTPError as exc:
                health.append({"at": at, "http": exc.code, "body": exc.read().decode()[:120]})
            except Exception as exc:
                health.append({"at": at, "http": None, "error": type(exc).__name__})
            stop.wait(0.25)

    sb.docker = traced
    poller = threading.Thread(target=poll, daemon=True)
    poller.start()
    try:
        sb.storage_failure(evidence)
    finally:
        stop.set()
        poller.join(5)
        sb.docker = original
        evidence["docker_timeline"] = timeline
        # keep every status change plus one sample per second to bound the size
        kept, last, last_kept = [], None, 0.0
        for h in health:
            if h.get("http") != last or h["at"] - last_kept >= 1.0:
                kept.append(h)
                last, last_kept = h.get("http"), h["at"]
        evidence["healthz_samples"] = kept
        counts = {}
        for h in health:
            counts[str(h.get("http"))] = counts.get(str(h.get("http")), 0) + 1
        evidence["healthz_summary"] = counts


RESUME_MODE = "api"


def set_fault(mode):
    sb.docker("fault-proxy", "exec", "python", "-c", f"from pathlib import Path; Path('/tmp/bus-fault-mode').write_text('{mode}')")


def api_resume(run_id):
    req = urllib.request.Request(sb.BASE + "/api/resume", data=json.dumps({"run": run_id, "user": "alice", "password": PASSWORD}).encode(),
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return {"at": time.time(), "http": resp.status, "body": resp.read().decode()[:200]}
    except urllib.error.HTTPError as exc:
        return {"at": time.time(), "http": exc.code, "body": exc.read().decode()[:200]}


def resume_once(run_id, evidence, label):
    """api: resume through the same endpoint the plugin uses. external: wait up
    to 15 minutes for an operator to resume from the plugin page."""
    if RESUME_MODE == "api":
        result = api_resume(run_id)
    else:
        print(f"{label}: waiting for an operator to resume {run_id} from the plugin", flush=True)
        deadline = time.monotonic() + 900
        while time.monotonic() < deadline and sb.read_run(run_id)["status"] == "waiting":
            time.sleep(1)
        resumed = sb.read_run(run_id)["status"] != "waiting"
        result = {"at": time.time(), "http": None, "body": "resumed externally" if resumed else "timeout"}
    evidence.setdefault("resumes", []).append({"label": label, **result})
    return result


def summarize(run_id, evidence):
    run = sb.read_run(run_id)
    evidence["run"] = sb.redact(run)
    after = sb.call_snapshot(run)
    sends, events = transport_sends({c["request_id"] for c in after.values()}, {c["remote_task_id"] for c in after.values() if c["remote_task_id"]})
    evidence["transport_events"] = events
    return run, after, sends


def kept_identity(before, after, keys):
    return all(after[k]["request_id"] == before[k]["request_id"] and
               (not before[k]["remote_task_id"] or after[k]["remote_task_id"] == before[k]["remote_task_id"]) for k in keys)


def ra_completed_then_unknown(evidence, at_report=False):
    run_id, plan_id = sb.new_run("ra-report" if at_report else "ra")
    evidence.update(run_id=run_id, plan_id=plan_id, variant="unknown-at-report" if at_report else "unknown-after-two")
    state = {"armed": False}

    def hook(graph):
        if state["armed"]:
            return
        status = {n["id"]: n.get("status") for n in graph["nodes"]}
        done_remote = [n["id"] for n in graph["nodes"] if n.get("remote") and n.get("status") == "done"]
        ready = (all(status.get(k) == "done" for k in ("intake", "financial", "industry", "bureau", "related"))
                 if at_report else len(done_remote) >= 2)
        if ready:
            set_fault("drop-query-once")
            state.update(armed=True, at=time.time(), done_remote=done_remote)

    graph = sb.wait_run(run_id, evidence, hook, timeout=600)
    evidence["armed"] = dict(state)
    run = sb.read_run(run_id)
    before = sb.call_snapshot(run)
    completed_before = [k for k, s in run["steps"].items() if s["status"] == "done" and k in before]
    evidence.update(status_before_resume=graph["run"]["status"], before_resume=before, completed_before=completed_before,
                    unknown_before=[k for k, s in run["steps"].items() if s["status"] == "unknown"])
    if graph["run"]["status"] == "waiting":
        resume_once(run_id, evidence, "resume")
        graph = sb.wait_run(run_id, evidence, timeout=600)
    run, after, sends = summarize(run_id, evidence)
    evidence["checks"] = {
        "armed_after_two_completed": state["armed"],
        "was_waiting_with_unknown": evidence["status_before_resume"] == "waiting" and bool(evidence["unknown_before"]),
        "completed_before": completed_before,
        "completed_nodes_unchanged": len(completed_before) >= 2 and all(after[k][f] == before[k][f] for k in completed_before
                                                                       for f in ("request_id", "artifact_id", "artifact_digest", "model_events")),
        "others_kept_request_and_task": kept_identity(before, after, [k for k in before if k not in completed_before]),
        "one_send_per_request": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
        "final_status": graph["run"]["status"],
    }


def rc_resume_storage_failure(evidence):
    set_fault("drop-query-once")
    run_id, plan_id = sb.new_run("rc")
    evidence.update(run_id=run_id, plan_id=plan_id)
    graph = sb.wait_run(run_id, evidence)
    evidence["status_before_fault"] = graph["run"]["status"]
    before = sb.call_snapshot(sb.read_run(run_id))
    evidence["before_fault"] = before
    injected = restored = False
    first = {}
    try:
        evidence["injected_at"] = time.time()
        sb.docker("credit", "exec", "python", "-c", "from pathlib import Path; p=Path('/data/state.json'); backup=p.with_suffix('.bus-test-backup'); assert not backup.exists(); p.rename(backup); p.mkdir()")
        injected = True
        first = resume_once(run_id, evidence, "resume-during-storage-fault")
        try:
            with urllib.request.urlopen(sb.BASE + "/healthz", timeout=5) as resp:
                evidence["healthz_after_resume"] = {"at": time.time(), "http": resp.status, "body": resp.read().decode()[:120]}
        except urllib.error.HTTPError as exc:
            evidence["healthz_after_resume"] = {"at": time.time(), "http": exc.code, "body": exc.read().decode()[:120]}
    finally:
        if injected:
            evidence["restore_at"] = time.time()
            sb.docker("credit", "exec", "python", "-c", "from pathlib import Path; p=Path('/data/state.json'); p.rmdir(); p.with_suffix('.bus-test-backup').rename(p)")
            sb.docker("credit", "restart")
            evidence["credit_healthy_again"] = wait_healthy("credit")
            restored = True
    status_after_restore = sb.read_run(run_id)["status"]
    evidence["status_after_restore"] = status_after_restore
    second = resume_once(run_id, evidence, "resume-after-restore") if status_after_restore == "waiting" else None
    graph = sb.wait_run(run_id, evidence, timeout=600)
    run, after, sends = summarize(run_id, evidence)
    evidence["checks"] = {
        "was_waiting": evidence["status_before_fault"] == "waiting",
        "resume_refused_during_fault": first.get("http") == 503 if RESUME_MODE == "api" else None,
        "health_503_during_fault": evidence.get("healthz_after_resume", {}).get("http") == 503,
        "restored": restored,
        "still_waiting_after_restore": status_after_restore == "waiting",
        "second_resume_accepted": bool(second) and (second.get("http") == 200 or RESUME_MODE != "api"),
        "kept_request_and_task": kept_identity(before, after, list(before)),
        "one_send_per_request": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
        "final_status": graph["run"]["status"],
    }


def rb_send_reply_lost(evidence):
    set_fault("drop-send-reply-once")
    run_id, plan_id = sb.new_run("rb-send")
    evidence.update(run_id=run_id, plan_id=plan_id)
    graph = sb.wait_run(run_id, evidence)
    run = sb.read_run(run_id)
    before = sb.call_snapshot(run)
    evidence.update(status_before_resume=graph["run"]["status"], before_resume=before)
    first_events = transport_sends({c["request_id"] for c in before.values()})[1]
    forwarded = [e for e in first_events if e.get("forwarded")]
    evidence["forwarded_send"] = forwarded
    resume_once(run_id, evidence, "resume")
    graph = sb.wait_run(run_id, evidence, timeout=300)
    run, after, sends = summarize(run_id, evidence)
    lost = [k for k, c in before.items() if not c["remote_task_id"]]
    evidence["checks"] = {
        "was_waiting": evidence["status_before_resume"] == "waiting",
        "receipt_lost_node": lost,
        "remote_task_created_upstream": bool(forwarded) and bool(forwarded[0]["forwarded"].get("remote_task_id")),
        "no_resend_after_resume": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
        "still_unresolved_after_resume": graph["run"]["status"] == "waiting" and bool(lost) and all(not after[k]["remote_task_id"] for k in lost),
        "final_status": graph["run"]["status"],
        "note": "without a remote task id the call stays UNKNOWN and is never resent; the upstream task is an orphan that needs manual reconciliation",
    }


def rb_resume_reply_lost(evidence):
    import socket
    set_fault("drop-query-once")
    run_id, plan_id = sb.new_run("rb-resume")
    evidence.update(run_id=run_id, plan_id=plan_id)
    graph = sb.wait_run(run_id, evidence)
    run = sb.read_run(run_id)
    before = sb.call_snapshot(run)
    evidence.update(status_before_resume=graph["run"]["status"], before_resume=before, leases_before=len(run.get("leases", [])))
    # send the resume and drop the connection without reading the reply
    parsed = urllib.parse.urlparse(sb.BASE)
    payload = json.dumps({"run": run_id, "user": "alice", "password": PASSWORD}).encode()
    head = (f"POST /api/resume HTTP/1.1\r\nHost: {parsed.netloc}\r\nContent-Type: application/json\r\n"
            f"Content-Length: {len(payload)}\r\nConnection: close\r\n\r\n").encode()
    sock = socket.create_connection((parsed.hostname, parsed.port or 80), timeout=5)
    sock.sendall(head + payload)
    sock.close()
    evidence["resume_sent_and_dropped_at"] = time.time()
    time.sleep(1.5)
    status_after_drop = sb.read_run(run_id)["status"]
    evidence["status_after_dropped_reply"] = status_after_drop
    retry = api_resume(run_id)
    evidence["retry"] = retry
    graph = sb.wait_run(run_id, evidence, timeout=600)
    run, after, sends = summarize(run_id, evidence)
    evidence["checks"] = {
        "was_waiting": evidence["status_before_resume"] == "waiting",
        "resume_persisted_without_reply": status_after_drop in ("queued", "running"),
        "retry_has_no_second_effect": retry.get("http") == 409,
        "one_new_lease": len(run.get("leases", [])) == evidence["leases_before"] + 1,
        "kept_request_and_task": kept_identity(before, after, list(before)),
        "one_send_per_request": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
        "final_status": graph["run"]["status"],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("scenario", choices=["go-kill", "two-clients", "storage-failure-traced", "ra-completed-then-unknown", "ra-unknown-at-report",
                                             "rc-resume-storage-failure", "rb-send-reply-lost", "rb-resume-reply-lost"])
    parser.add_argument("--resume", choices=["api", "external"], default="api",
                        help="api: resume through /api/resume (the plugin's endpoint); external: wait for an operator in the plugin")
    args = parser.parse_args()
    global RESUME_MODE
    RESUME_MODE = args.resume
    evidence = {"scenario": args.scenario, "started_at": time.time(), "script": "evidence/pr30-w05/scripts/w05_rd_extra.py",
                "scope": "W05 single-side R-D extras on synthetic credit data", "snapshots": [], "environment": sb.environment_record()}
    directory = sb.CACHE / "evidence" / "agent-bus"
    directory.mkdir(parents=True, exist_ok=True)
    target = directory / f"{args.scenario}-{int(time.time())}.json"
    try:
        {"go-kill": go_kill, "two-clients": two_clients, "storage-failure-traced": storage_failure_traced,
         "ra-completed-then-unknown": ra_completed_then_unknown,
         "ra-unknown-at-report": lambda ev: ra_completed_then_unknown(ev, at_report=True), "rc-resume-storage-failure": rc_resume_storage_failure,
         "rb-send-reply-lost": rb_send_reply_lost, "rb-resume-reply-lost": rb_resume_reply_lost}[args.scenario](evidence)
    finally:
        evidence["finished_at"] = time.time()
        target.write_text(json.dumps(sb.redact(evidence), ensure_ascii=False, indent=2) + "\n")
        print("Evidence:", target)
        print("checks:", json.dumps(evidence.get("checks"), ensure_ascii=False))


if __name__ == "__main__":
    main()
