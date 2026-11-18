"""Watch the R-A scenario output; when it waits for an operator, take a JVM
thread dump of the orchestrator, then resume through /api/resume (the plugin's
endpoint). Prints JSON lines; never prints the password."""
import json, os, re, subprocess, sys, time, urllib.request

out_path, base, project = sys.argv[1], sys.argv[2], sys.argv[3]
orch = f"{project}-orchestrator-1"
def stamp(**kv):
    print(json.dumps({"at": time.time(), **kv}), flush=True)
def identity():
    insp = subprocess.run(["docker", "inspect", "-f", "{{.State.StartedAt}} {{.RestartCount}} {{.State.Pid}}", orch], capture_output=True, text=True).stdout.split()
    stat = subprocess.run(["docker", "exec", orch, "cat", "/proc/1/stat"], capture_output=True, text=True).stdout.split()
    return {"container_started_at": insp[0], "restart_count": int(insp[1]), "host_pid": int(insp[2]), "pid1_starttime_ticks": stat[21] if len(stat) > 21 else None}
stamp(event="identity_before", **identity())
deadline = time.time() + 900
run = None
while time.time() < deadline:
    text = open(out_path, encoding="utf-8", errors="replace").read() if os.path.exists(out_path) else ""
    m = re.search(r"waiting for an operator to resume (R\d+)", text)
    if m:
        run = m.group(1); break
    if "Evidence:" in text or "Traceback" in text:
        break
    time.sleep(0.5)
if not run:
    stamp(event="no_waiting_seen"); sys.exit(1)
stamp(event="waiting_seen", run=run)
time.sleep(2)
stamp(event="thread_dump_requested")
subprocess.run(["docker", "kill", "--signal=QUIT", orch], capture_output=True)
time.sleep(2)
stamp(event="identity_at_waiting", **identity())
pw = os.environ["CREDIT_DEMO_PASSWORD"]
req = urllib.request.Request(base + "/api/resume", data=json.dumps({"run": run, "user": "alice", "password": pw}).encode(),
                             headers={"Content-Type": "application/json"})
try:
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=15) as r:
        stamp(event="resumed", http=r.status)
except urllib.error.HTTPError as e:
    stamp(event="resume_refused", http=e.code, body=e.read().decode()[:200])
while time.time() < deadline:
    text = open(out_path, encoding="utf-8", errors="replace").read()
    if "Evidence:" in text or "Traceback" in text:
        break
    time.sleep(1)
stamp(event="identity_after", **identity())
