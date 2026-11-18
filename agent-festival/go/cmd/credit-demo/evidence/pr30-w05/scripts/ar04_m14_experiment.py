#!/usr/bin/env python3
"""AR-04 M1-M3 experiment on a separate compose project (test only).

Run from deploy/ of a checkout of the candidate. Phases:
  m3       a run with a judge node is in flight on the OLD images (no binding);
           the executor is killed; the stack is upgraded to the candidate images.
           Expected: the run stops as blocked on takeover, nothing new is sent.
  m1-local a run bound to model A; the orchestrator is configured with model B.
           Expected: lease refused, run blocked, zero model requests.
  m1-remote orchestrator on A, professional runtimes on B.
           Expected: the runtime is stopped before its model call, run blocked,
           zero model requests from the runtimes.
  m2       everything on A. Expected: the run completes; every model request at
           the egress names A; each remote model event records A.
Model requests are observed at the model-egress recorder (request model field,
caller, status only). Evidence: $DEMO_CACHE/evidence/agent-bus/ar04-m14-<ts>.json.

Required env: M14_OLD_{CREDIT,ORCH,AGENT}_IMAGE, M14_NEW_{CREDIT,ORCH,AGENT}_IMAGE,
M14_MODEL_A, M14_MODEL_B, W05_MODEL_EGRESS, CREDIT_DEMO_PASSWORD.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

PROJECT = os.environ.get("M14_PROJECT", "agent-hub-m14")
os.environ.update(COMPOSE_PROJECT_NAME=PROJECT, CREDIT_PORT="5291",
                  BUS_TEST_BASE="http://127.0.0.1:5291", BUS_TEST_PROJECT=PROJECT)
sys.path.insert(0, os.getcwd())
import demo  # noqa: E402
import smoke_bus as sb  # noqa: E402

demo.BUS = True
OVERLAY = Path(os.environ["W05_MODEL_EGRESS"]).with_name("compose.w05-m14.yaml")
CORE = ["credit", "bus-db", "rdc", "gateway", "material-agent", "analysis-agent", "writer-agent", "model-egress"]
A, B = os.environ["M14_MODEL_A"], os.environ["M14_MODEL_B"]


def configure(images, credit, orchestrator, runtime):
    kind = "OLD" if images == "old" else "NEW"
    for part in ("CREDIT", "ORCH", "AGENT"):
        os.environ[f"M14_{part}_IMAGE"] = os.environ[f"M14_{kind}_{part}_IMAGE"]
    os.environ.update(M14_MODEL_CREDIT=credit, M14_MODEL_ORCHESTRATOR=orchestrator, M14_MODEL_RUNTIME=runtime)


def compose(*args):
    return demo.compose("-f", OVERLAY, *args)


def up(*services):
    compose("up", "-d", "--no-build", "--wait", *services)


def egress_lines():
    out = subprocess.run(["docker", "exec", f"{PROJECT}-model-egress-1", "sh", "-c", "cat /tmp/model-egress.jsonl 2>/dev/null || true"],
                         capture_output=True, text=True).stdout
    return [json.loads(l) for l in out.splitlines() if l.strip()]


def callers():
    ids = subprocess.run(["docker", "ps", "-q", "--filter", f"label=com.docker.compose.project={PROJECT}"], capture_output=True, text=True).stdout.split()
    names = {}
    for c in json.loads(subprocess.run(["docker", "inspect", *ids], capture_output=True, text=True).stdout) if ids else []:
        for net in c["NetworkSettings"]["Networks"].values():
            names[net["IPAddress"]] = c["Config"]["Labels"].get("com.docker.compose.service")
    return names


def egress_since(t0):
    """request_started events (recorded before forwarding) since t0, with the
    calling service; finished/error events are joined by request_id."""
    names = callers()
    lines = egress_lines()
    done = {e["request_id"]: e for e in lines if e.get("event") in ("response_finished", "error")}
    out = []
    for e in lines:
        if e.get("event") == "request_started" and e["at"] >= t0:
            end = done.get(e["request_id"], {})
            out.append({**e, "service": names.get(e["caller"], e["caller"]), "status": end.get("status"),
                        "finished": end.get("event"), "duration": end.get("duration")})
    return out


def run_record(run_id):
    run = sb.read_run(run_id)
    return {"status": run["status"], "final": run.get("final_output"), "leases": run.get("leases", []),
            "lease_refusals": run.get("lease_refusals", []),
            "binding": run.get("exec_binding"),
            "calls": {k: {f: c.get(f) for f in ("request_id", "remote_task_id", "status", "reason", "model_events", "actual_models")}
                      for k, s in run["steps"].items() if (c := s.get("remote_call"))},
            "steps": {k: s["status"] for k, s in run["steps"].items()}}


def wait_status(run_id, wanted, timeout):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status = sb.read_run(run_id)["status"]
        if status in wanted:
            return status
        time.sleep(1)
    return sb.read_run(run_id)["status"]


def capture_failure(run_id, t0, rec):
    """Logs and the remote task's own status, taken before any container changes."""
    since = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(t0 - 5))
    logs = {}
    for svc in ("material-agent", "analysis-agent", "writer-agent", "gateway", "orchestrator"):
        out = subprocess.run(["docker", "logs", "--since", since, f"{PROJECT}-{svc}-1"], capture_output=True, text=True)
        logs[svc] = (out.stdout + out.stderr).splitlines()[-200:]
    tasks = {}
    for step, c in rec["calls"].items():
        if c.get("remote_task_id"):
            script = ("import json,os,urllib.request;"
                      "b=json.dumps({'jsonrpc':'2.0','id':'diag','method':'GetTask','params':{'id':os.environ['T']}}).encode();"
                      "r=urllib.request.Request(os.environ['CREDIT_BUS_GATEWAY'].rstrip('/')+'/a2a',data=b,"
                      "headers={'Content-Type':'application/json','Authorization':'Bearer '+os.environ['CREDIT_BUS_TOKEN']});"
                      "t=json.load(urllib.request.urlopen(r,timeout=10));t=t.get('result',t);t=t.get('task',t);"
                      "print(json.dumps({'state':t.get('status',{}).get('state'),'message':json.dumps(t.get('status',{}).get('message'))[:800]}))")
            out = subprocess.run(["docker", "exec", "-e", f"T={c['remote_task_id']}", f"{PROJECT}-orchestrator-1", "python", "-c", script],
                                 capture_output=True, text=True)
            tasks[step] = (out.stdout or out.stderr).strip()[:1200]
    return {"logs": logs, "remote_tasks": tasks}


def phase_m3(ev):
    configure("old", A, A, A)
    up(*CORE, "orchestrator")
    demo.wait_bus()
    run_id, _ = sb.new_run("m14-m3-old-head")
    deadline = time.monotonic() + 240
    while time.monotonic() < deadline:
        graph, _ = sb.request(f"/api/graph?run={run_id}")
        intake = next(n for n in graph["nodes"] if n["id"] == "intake")
        if intake.get("remote", {}).get("task_id"):
            break
        time.sleep(0.5)
    before = run_record(run_id)
    subprocess.run(["docker", "kill", f"{PROJECT}-orchestrator-1"], capture_output=True)
    compose("stop")
    configure("new", A, A, A)
    t0 = time.time()
    up(*CORE, "orchestrator")
    status = wait_status(run_id, {"blocked", "done", "failed"}, 180)
    after = run_record(run_id)
    egress = egress_since(t0)
    ev["m3"] = {"run": run_id, "before_upgrade": before, "after_upgrade": after, "egress_after_upgrade": egress,
                "checks": {"blocked": status == "blocked", "reason_names_unresolvable_spec": "规约" in (after["final"] or ""),
                           "no_new_lease_after_upgrade": len(after["leases"]) == len(before["leases"]),
                           "no_model_request_after_upgrade": not egress,
                           "no_new_remote_call": set(after["calls"]) == set(before["calls"])}}


def phase_m1_local(ev):
    configure("new", A, B, A)
    up("orchestrator")
    t0 = time.time()
    run_id, _ = sb.new_run("m14-m1-local")
    deadline = time.monotonic() + 90  # the model-B orchestrator polls; each poll is refused
    while time.monotonic() < deadline and not run_record(run_id)["lease_refusals"]:
        time.sleep(1)
    time.sleep(10)
    rec = run_record(run_id)
    egress = egress_since(t0)
    refusal = (rec["lease_refusals"] or [{}])[0]
    ev["m1_local"] = {"run": run_id, "record": rec, "egress": egress,
                      "checks": {"still_queued_for_a_matching_executor": rec["status"] == "queued",
                                 "refusal_names_both_models": refusal.get("model") == B and refusal.get("bound") == A,
                                 "refused_polls": refusal.get("count"),
                                 "never_leased": not rec["leases"], "no_remote_call": not rec["calls"], "no_model_request_started": not egress}}


def phase_m1_remote(ev):
    configure("new", A, A, B)
    up(*CORE, "orchestrator")  # unchanged services stay as they are; a fresh project needs them all
    demo.wait_bus()
    t0 = time.time()
    run_id, _ = sb.new_run("m14-m1-remote")
    status = wait_status(run_id, {"blocked", "done", "failed"}, 240)
    time.sleep(3)
    rec = run_record(run_id)
    egress = egress_since(t0)
    runtime_requests = [e for e in egress if e["service"] in ("material-agent", "analysis-agent", "writer-agent")]
    ev.setdefault("m1_remote_after_block", {})["note"] = "a blocked run refuses every node credential (P1); see the Go review tests"
    intake = rec["calls"].get("intake", {})
    ev["m1_remote"] = {"run": run_id, "record": rec, "egress": egress,
                       "checks": {"blocked": status == "blocked", "reason_names_both_models": A in (rec["final"] or "") and B in (rec["final"] or ""),
                                  "no_model_event_recorded": not intake.get("model_events"),
                                  "no_runtime_model_request": not runtime_requests,
                                  "no_request_for_model_b": not [e for e in egress if e["model"] == B]}}


RUNTIMES = ("material-agent", "analysis-agent", "writer-agent")


def runtime_ips():
    """Each runtime container's address on the project network ("" if not running)."""
    return {s: subprocess.run(["docker", "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", f"{PROJECT}-{s}-1"],
                              capture_output=True, text=True).stdout.strip() for s in RUNTIMES}


def phase_m2(ev):
    ips_before = runtime_ips()
    configure("new", A, A, A)
    up(*CORE, "orchestrator")
    demo.wait_bus()
    settle = int(os.environ.get("M14_SETTLE", "0"))
    time.sleep(settle)
    t0 = time.time()
    ev["m2_ready"] = {"at": time.time(), "settle_seconds": settle, "ips_before_recreate": ips_before, "ips_after_recreate": runtime_ips(),
                      "runtimes": {s: subprocess.run(["docker", "inspect", "--format", "{{.State.Health.Status}} {{.State.StartedAt}}", f"{PROJECT}-{s}-1"],
                                                                  capture_output=True, text=True).stdout.strip()
                                                   for s in ("material-agent", "analysis-agent", "writer-agent")}}
    run_id, _ = sb.new_run("m14-m2")
    status = wait_status(run_id, {"blocked", "done", "failed", "waiting"}, 600)
    rec = run_record(run_id)
    if status != "done":
        ev["m2_failure"] = capture_failure(run_id, t0, rec)
    egress = egress_since(t0)
    actual = [m for c in rec["calls"].values() for m in (c.get("actual_models") or {}).values()]
    ev["m2"] = {"run": run_id, "record": rec, "egress": egress,
                "checks": {"completed": status == "done", "egress_requests": len(egress),
                           "all_egress_on_model_a": bool(egress) and all(e["model"] == A for e in egress),
                           "remote_events_record_model_a": bool(actual) and all(m == A for m in actual),
                           "binding_model_a": (rec["binding"] or {}).get("model") == A}}


def main():
    phases = sys.argv[1:] or ["m3", "m1-local", "m1-remote", "m2"]
    ev = {"scenario": "ar04-m14", "project": PROJECT, "started_at": time.time(), "phases": phases, "models": {"A": A, "B": B},
          "images": {k: os.environ[k] for k in os.environ if k.startswith("M14_") and k.endswith("_IMAGE")}}
    target = sb.CACHE / "evidence" / "agent-bus" / f"ar04-m14-{int(time.time())}.json"
    target.parent.mkdir(parents=True, exist_ok=True)
    try:
        for p in phases:
            {"m3": phase_m3, "m1-local": phase_m1_local, "m1-remote": phase_m1_remote, "m2": phase_m2}[p](ev)
            print(p, json.dumps(ev[p.replace("-", "_")]["checks"], ensure_ascii=False), flush=True)
    finally:
        ev["finished_at"] = time.time()
        ev["environment"] = sb.environment_record()
        target.write_text(json.dumps(sb.redact(ev), ensure_ascii=False, indent=2) + "\n")
        print("Evidence:", target)


if __name__ == "__main__":
    main()
