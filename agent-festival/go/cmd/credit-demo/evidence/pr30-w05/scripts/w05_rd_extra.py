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
  rb-send-reply-lost-restart
               rb-send-reply-lost, then restart the Go control service and the
               orchestrator: request id and send count are kept across the
               restart and the call is still never resent.
  adopt-snapshot-fail
               fail the state write while the local judge is running, so the
               adoption reaches the ledger but not the snapshot; repair and
               restart: the run is blocked with its reason, the ledger keeps the
               original operation, no decision is regenerated, lease and resume
               are refused, remote calls are not resent.
  history-read-no-lease
               finish a run, read it through every read entry (plugin API,
               signed-in pages) from two clients and restart the services: no
               lease, epoch, state revision or send changes.

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


def state_rev():
    state, _ = sb.request("/debug/state")
    return state.get("rev")


def restart_services(evidence, label):
    evidence.setdefault("restarts", []).append({"label": label, "at": time.time()})
    sb.docker("credit", "restart")
    healthy = wait_healthy("credit")
    sb.docker("orchestrator", "restart")
    healthy = wait_healthy("orchestrator") and healthy
    evidence["restarts"][-1].update(healthy=healthy, ready_at=time.time())
    return healthy


def data_file(path):
    """Text of a file in the credit data volume, read through the container, or None."""
    try:
        return sb.docker("credit", "exec", "cat", path)
    except Exception:
        return None


def rb_send_reply_lost_restart(evidence):
    set_fault("drop-send-reply-once")
    run_id, plan_id = sb.new_run("rb-send-restart")
    evidence.update(run_id=run_id, plan_id=plan_id)
    graph = sb.wait_run(run_id, evidence)
    run = sb.read_run(run_id)
    before = sb.call_snapshot(run)
    evidence.update(status_before_restart=graph["run"]["status"], before_restart=before,
                    epoch_before=run.get("epoch"), leases_before=len(run.get("leases", [])))
    first_events = transport_sends({c["request_id"] for c in before.values()})[1]
    evidence["forwarded_send"] = [e for e in first_events if e.get("forwarded")]
    restart_services(evidence, "credit+orchestrator")
    time.sleep(20)
    run = sb.read_run(run_id)
    mid = sb.call_snapshot(run)
    evidence.update(status_after_restart=run["status"], after_restart=mid)
    resume_once(run_id, evidence, "resume-after-restart")
    graph = sb.wait_run(run_id, evidence, timeout=300)
    run, after, sends = summarize(run_id, evidence)
    lost = [k for k, c in before.items() if not c["remote_task_id"]]
    forwarded = evidence["forwarded_send"]
    evidence["checks"] = {
        "was_waiting": evidence["status_before_restart"] == "waiting",
        "receipt_lost_node": lost,
        "remote_task_created_upstream": bool(forwarded) and bool(forwarded[0]["forwarded"].get("remote_task_id")),
        "waiting_after_restart": evidence["status_after_restart"] == "waiting",
        "request_kept_across_restart": kept_identity(before, mid, list(before)) and kept_identity(before, after, list(before)),
        "no_resend": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
        "still_unresolved": graph["run"]["status"] == "waiting" and bool(lost) and all(not after[k]["remote_task_id"] for k in lost),
        "final_status": graph["run"]["status"],
    }


def adopt_snapshot_fail(evidence):
    run_id, plan_id = sb.new_run("adopt-snapshot")
    evidence.update(run_id=run_id, plan_id=plan_id)
    ledger = f"/data/runs/{run_id}/adopt.jsonl"
    deadline = time.monotonic() + 600
    run = None
    while time.monotonic() < deadline:
        run = sb.read_run(run_id)
        if run["steps"]["judge"]["status"] == "running" or run["status"] not in ("queued", "running"):
            break
        time.sleep(0.3)
    evidence["judge_running_seen"] = bool(run) and run["steps"]["judge"]["status"] == "running"
    before = sb.call_snapshot(run)
    evidence.update(before_fault=before, steps_before={k: s["status"] for k, s in run["steps"].items()},
                    epoch_before=run.get("epoch"), leases_before=len(run.get("leases", [])),
                    ledger_before=data_file(ledger))
    injected = restored = False
    try:
        evidence["injected_at"] = time.time()
        sb.docker("credit", "exec", "python", "-c", "from pathlib import Path; p=Path('/data/state.json'); backup=p.with_suffix('.bus-test-backup'); assert not backup.exists(); p.rename(backup); p.mkdir()")
        injected = True
        frozen = None
        for _ in range(240):
            try:
                sb.request("/healthz")
            except urllib.error.HTTPError as exc:
                if exc.code == 503:
                    frozen = time.time()
                    break
            except ValueError:
                pass
            time.sleep(0.5)
        evidence["frozen_at"] = frozen
        time.sleep(2)
        evidence["ledger_during_fault"] = data_file(ledger)
    finally:
        if injected:
            evidence["restore_at"] = time.time()
            sb.docker("credit", "exec", "python", "-c", "from pathlib import Path; p=Path('/data/state.json'); p.rmdir(); p.with_suffix('.bus-test-backup').rename(p)")
            restored = True
            restart_services(evidence, "credit+orchestrator after repair")
    time.sleep(45)
    run = sb.read_run(run_id)
    graph, _ = sb.request(f"/api/graph?plan={plan_id}&v=1")
    plans, _ = sb.request("/api/plans")
    rows = plans if isinstance(plans, list) else plans.get("plans", [])
    evidence.update(status_after_restart=run["status"], final_after_restart=(run.get("final_output") or "")[:300],
                    graph_run_after=graph.get("run"), plan_row_after=[p for p in rows if p.get("run") == run_id],
                    ledger_after=data_file(ledger), epoch_after=run.get("epoch"), leases_after=len(run.get("leases", [])),
                    decision_after=run.get("decision"), judge_after=run["steps"]["judge"])
    resume = api_resume(run_id)
    evidence["resume_after_restart"] = resume
    time.sleep(30)
    run2 = sb.read_run(run_id)
    evidence.update(status_after_resume_attempt=run2["status"], epoch_after_wait=run2.get("epoch"), leases_after_wait=len(run2.get("leases", [])))
    files = sb.docker("credit", "exec", "sh", "-c", "ls /data").split()
    evidence["data_files"] = files
    evidence["dispositions"] = [json.loads(data_file("/data/" + f) or "{}") for f in files if f.startswith("disposition-")]
    run, after, sends = summarize(run_id, evidence)
    blocked = (graph.get("run") or {}).get("blocked") or {}
    ledger_lines = [json.loads(line) for line in (evidence["ledger_during_fault"] or "").splitlines() if line.strip()]
    adopted = [line for line in ledger_lines if (line.get("outcome") or {}).get("status") == "adopted"]
    evidence["checks"] = {
        "judge_running_when_injected": evidence["judge_running_seen"],
        "service_froze": bool(evidence["frozen_at"]),
        "adoption_reached_ledger": bool(adopted),
        "ledger_operation": [(line.get("identity") or {}).get("operation_id") for line in adopted],
        "ledger_unchanged_by_restart": evidence["ledger_during_fault"] is not None and evidence["ledger_during_fault"] == evidence["ledger_after"],
        "restored": restored,
        "blocked_after_restart": evidence["status_after_restart"] == "blocked",
        "blocked_projection": {k: blocked.get(k) for k in ("code", "cause", "disposition", "source")},
        "decision_not_regenerated": not evidence["decision_after"] and evidence["judge_after"].get("status") != "done",
        "resume_refused": resume.get("http") not in (200, None),
        "not_leased_after_restart": run2["status"] == "blocked" and evidence["leases_after_wait"] == evidence["leases_after"],
        "disposition_recorded": any(r.get("run") == run_id and r.get("disposition") == "BLOCKED"
                                    for d in evidence["dispositions"] for r in d.get("records", [])),
        "kept_request_and_task": kept_identity(before, after, list(before)),
        "one_send_per_request": bool(sends) and all(n == 1 for n in sends.values()),
        "send_counts": sends,
    }


def history_read_no_lease(evidence):
    run_id, plan_id = sb.new_run("history-read")
    evidence.update(run_id=run_id, plan_id=plan_id)
    graph = sb.wait_run(run_id, evidence, timeout=600)
    time.sleep(5)
    run = sb.read_run(run_id)
    before = sb.call_snapshot(run)
    sends_before = transport_sends({c["request_id"] for c in before.values()},
                                   {c["remote_task_id"] for c in before.values() if c["remote_task_id"]})[0]

    def snap(r):
        return {"status": r["status"], "epoch": r.get("epoch"), "leases": len(r.get("leases", [])),
                "final_output_len": len(r.get("final_output") or ""), "token_set": bool(r.get("token"))}

    evidence.update(status_done=graph["run"]["status"], before_reads=snap(run), rev_before=state_rev(), sends_before=sends_before)
    sessions = []
    for _ in range(2):
        cookies = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
        try:
            opener.open(sb.BASE + "/ui/login", urllib.parse.urlencode({"user": "alice", "password": PASSWORD, "next": "/ui/"}).encode(), timeout=10).close()
        except urllib.error.HTTPError as exc:
            if exc.code != 404:
                raise
        sessions.append(opener)
    reads = []

    def read_all(opener, n):
        for path in (f"/api/graph?plan={plan_id}&v=1", "/api/plans", "/ui/", f"/ui/plan/{plan_id}/v1", f"/ui/run/{run_id}"):
            try:
                with opener.open(sb.BASE + path, timeout=10) as resp:
                    reads.append({"client": n, "path": path.split("?")[0], "http": resp.status})
            except urllib.error.HTTPError as exc:
                reads.append({"client": n, "path": path.split("?")[0], "http": exc.code})

    for _ in range(5):
        threads = [threading.Thread(target=read_all, args=(o, i)) for i, o in enumerate(sessions)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
    evidence["reads"] = {"count": len(reads), "statuses": sorted({(r["path"], r["http"]) for r in reads})}
    run = sb.read_run(run_id)
    evidence.update(after_reads=snap(run), rev_after_reads=state_rev())
    restart_services(evidence, "credit+orchestrator")
    time.sleep(45)
    run = sb.read_run(run_id)
    evidence.update(after_restart=snap(run), rev_after_restart=state_rev())
    run, after, sends = summarize(run_id, evidence)
    evidence["checks"] = {
        "done_before_reads": evidence["status_done"] == "done",
        "reads": evidence["reads"]["count"],
        "unchanged_by_reads": evidence["before_reads"] == evidence["after_reads"] and evidence["rev_before"] == evidence["rev_after_reads"],
        "unchanged_by_restart": evidence["before_reads"] == evidence["after_restart"],
        "rev_before_reads_and_after_restart": [evidence["rev_before"], evidence["rev_after_restart"]],
        "no_new_send": sends == sends_before,
        "send_counts": sends,
        "kept_request_and_task": kept_identity(before, after, list(before)),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("scenario", choices=["go-kill", "two-clients", "storage-failure-traced", "ra-completed-then-unknown", "ra-unknown-at-report",
                                             "rc-resume-storage-failure", "rb-send-reply-lost", "rb-resume-reply-lost",
                                             "rb-send-reply-lost-restart", "adopt-snapshot-fail", "history-read-no-lease"])
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
         "rb-send-reply-lost": rb_send_reply_lost, "rb-resume-reply-lost": rb_resume_reply_lost,
         "rb-send-reply-lost-restart": rb_send_reply_lost_restart, "adopt-snapshot-fail": adopt_snapshot_fail,
         "history-read-no-lease": history_read_no_lease}[args.scenario](evidence)
    finally:
        evidence["finished_at"] = time.time()
        target.write_text(json.dumps(sb.redact(evidence), ensure_ascii=False, indent=2) + "\n")
        print("Evidence:", target)
        print("checks:", json.dumps(evidence.get("checks"), ensure_ascii=False))


if __name__ == "__main__":
    main()
