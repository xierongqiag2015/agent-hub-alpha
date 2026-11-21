"""Start (or stop) the five W06 role agents as detached native processes in WSL.
Usage: w06_services.py start|stop|status|ensure (ensure: start what is not running, plus the heartbeat). State in ~/w06-bus/services.json; logs in ~/w06-bus/logs/.
Model key/base are read from the private secrets file into the child environment only."""
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

HOME = Path.home() / "w06-bus"
JAR_SRC = Path(os.environ.get("W06_JAR", "/mnt/d/agent-hub-alpha/_deploy175/w06/out/w06-agent-runtime.jar"))  # build.sh output
JAVA = "/usr/lib/jvm/java-21-openjdk-amd64/bin/java"
MODEL = os.environ.get("W06_MODEL_NAME", "deepseek-v4-1-flash-260910")
ROLES = {"material": 18111, "metrics": 18112, "operation": 18113, "asset-quality": 18114, "writer": 18115}
STATE = HOME / "services.json"


def secrets():
    out = {}
    for line in (Path.home() / ".config/aha-swarm/secrets.env").read_text().splitlines():
        if "=" in line and not line.startswith("#"):
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


def identity(pid):
    stat = Path(f"/proc/{pid}/stat").read_text().split()
    return {"boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(), "start_ticks": int(stat[21])}


def card(port):
    for path in ("/.well-known/agent-card.json", "/a2a/.well-known/agent-card.json"):
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}{path}", timeout=3) as r:
                return path, json.loads(r.read())
        except Exception:
            pass
    return None, None


def start(only=None):
    HOME.mkdir(exist_ok=True)
    (HOME / "logs").mkdir(exist_ok=True)
    jar = HOME / "w06-agent-runtime.jar"
    if only is None or not jar.exists():
        shutil.copy(JAR_SRC, jar)
    s = secrets()
    state = json.loads(STATE.read_text()) if only is not None and STATE.exists() else {}
    for role, port in ROLES.items():
        if only is not None and role not in only:
            continue
        sid = "w06-" + role
        env = {k: v for k, v in os.environ.items() if k.lower() not in ("http_proxy", "https_proxy", "all_proxy")}
        env.update(AGENT_SERVICE_ID=sid, W06_ROLE=role, SERVER_PORT=str(port), SERVER_ADDRESS="127.0.0.1",
                   AGENT_PUBLIC_URL=f"http://127.0.0.1:{port}/a2a", W06_API_KEY=s["SWARM_API_KEY"], W06_API_BASE=s["SWARM_API_BASE"],
                   W06_MODEL_NAME=MODEL, W06_SNAPSHOT_DIR=str(HOME / "snapshot"))
        log = open(HOME / "logs" / f"{sid}.log", "ab")
        p = subprocess.Popen([JAVA, "-Xms64m", "-Xmx320m", "-jar", str(jar)], env=env, stdout=log, stderr=subprocess.STDOUT,
                             stdin=subprocess.DEVNULL, start_new_session=True, cwd=str(HOME))
        state[sid] = {"role": role, "port": port, "pid": p.pid, **identity(p.pid), "model": MODEL, "started_at": time.time()}
        print(json.dumps({"started": sid, "pid": p.pid, "port": port}), flush=True)
    STATE.write_text(json.dumps(state, indent=2))
    deadline = time.monotonic() + 150
    pending = {sid: rec for sid, rec in state.items() if only is None or rec["role"] in only}
    while pending and time.monotonic() < deadline:
        for sid, rec in list(pending.items()):
            path, c = card(rec["port"])
            if c:
                state[sid].update(card_path=path, card_name=c.get("name"), card_version=c.get("version"))
                print(json.dumps({"ready": sid, "card_path": path, "name": c.get("name"), "version": c.get("version")}), flush=True)
                pending.pop(sid)
        time.sleep(2)
    STATE.write_text(json.dumps(state, indent=2))
    for sid in pending:
        print(json.dumps({"not_ready": sid}), flush=True)


def owned(rec):
    try:
        return identity(rec["pid"]) == {"boot_id": rec["boot_id"], "start_ticks": rec["start_ticks"]}
    except FileNotFoundError:
        return False


def stop():
    state = json.loads(STATE.read_text())
    for sid, rec in state.items():
        if owned(rec):
            os.killpg(rec["pid"], signal.SIGTERM)
            print(json.dumps({"stopped": sid, "pid": rec["pid"]}))
        else:
            print(json.dumps({"not_running_or_not_ours": sid}))


def status():
    state = json.loads(STATE.read_text())
    for sid, rec in state.items():
        path, c = card(rec["port"])
        print(json.dumps({"service": sid, "pid": rec["pid"], "owned_and_running": owned(rec), "card": bool(c)}))


def ensure():
    """Start only the role agents that are not running (by recorded pid + boot id + start tick),
    then make sure the RDC heartbeat loop is running."""
    state = json.loads(STATE.read_text()) if STATE.exists() else {}
    missing = [role for role in ROLES if not (("w06-" + role) in state and owned(state["w06-" + role]))]
    if missing:
        start(missing)
    else:
        print(json.dumps({"w06_agents": "all running"}))
    hb = HOME / "heartbeat.json"
    if hb.exists() and owned(json.loads(hb.read_text())):
        print(json.dumps({"heartbeat": "running"}))
        return
    p = subprocess.Popen(["python3", str(Path(__file__).with_name("w06_bus_register.py")), "heartbeat"], cwd=str(HOME),
                         stdout=open(HOME / "logs" / "heartbeat.log", "ab"), stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                         start_new_session=True)
    hb.write_text(json.dumps({"pid": p.pid, **identity(p.pid)}))
    print(json.dumps({"heartbeat": "started", "pid": p.pid}))


{"start": start, "stop": stop, "status": status, "ensure": ensure}[sys.argv[1]]()
