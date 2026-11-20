"""Register the five W06 role agents in the annual environment's RDC, grant them to the worker
subject through a separate role (existing roles untouched), and keep them alive.
Usage: w06_bus_register.py register | heartbeat. Records go to ~/w06-bus/ (no secrets printed)."""
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

ROOT = Path.home() / "report-demo-env/repo/.local/report-demo"
HOME = Path.home() / "w06-bus"
cfg = json.loads((ROOT / "config.json").read_text())
identity = json.loads((ROOT / "identity.json").read_text())
bus = cfg["bus"]
services = json.loads((HOME / "services.json").read_text())
REG = HOME / "registrations.json"
ROLE = "w06-bus-trial"
registry = {"Authorization": "Bearer " + bus["registry_token"], "X-Tenant-ID": bus["tenant"]}


def http(method, url, body=None, headers=None, form=False):
    data = None
    h = dict(headers or {})
    if body is not None:
        data = urllib.parse.urlencode(body).encode() if form else json.dumps(body).encode()
        h["Content-Type"] = "application/x-www-form-urlencoded" if form else "application/json"
    req = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"text": raw.decode(errors="replace")[:300]}


def register_one(sid, rec):
    with urllib.request.urlopen(f"http://127.0.0.1:{rec['port']}/.well-known/agent-card.json", timeout=10) as r:
        raw_card = r.read().decode()
    card = json.loads(raw_card)
    assert card.get("name") == sid and card.get("version") == "1.0.0", card.get("name")
    st, out = http("POST", bus["rdc_url"] + f"/v2/agents/{sid}/instances", {
        "address": "127.0.0.1", "port": rec["port"], "endpointPath": "/a2a", "serviceId": sid, "maxConcurrency": 1,
        "frameworkType": "JIUWEN", "contractVersion": "1.0.0", "capabilityVersion": "1.0.0", "agentCardJson": raw_card}, registry)
    if st not in (200, 201):
        raise SystemExit(f"register {sid}: HTTP {st} {out}")
    return out


def register():
    regs = {sid: register_one(sid, rec) for sid, rec in services.items()}
    REG.write_text(json.dumps(regs, indent=2))
    admin = identity["clients"]["report-admin"]
    st, tok = http("POST", bus["token_url"], {"grant_type": "client_credentials", "client_id": admin["client_id"],
                                              "client_secret": admin["client_secret"]}, form=True)
    assert st == 200, st
    ah = {"Authorization": "Bearer " + tok["access_token"], "X-Tenant-ID": bus["tenant"]}
    base = bus["rdc_url"] + "/v2/authorization/admin"

    def call(method, path, body=None, allowed=()):
        st, out = http(method, base + path, body, {**ah, "Idempotency-Key": str(uuid.uuid4())})
        if st in allowed:
            return None
        if st >= 300:
            raise SystemExit(f"RDC admin {method} {path}: HTTP {st} {out}")
        return out

    record = {"role": ROLE, "agents": list(services)}
    if call("GET", f"/roles/{ROLE}", allowed=(404,)) is None:
        call("POST", "/roles", {"roleId": ROLE, "name": "W06 agents on Agent Bus (W05 trial)", "enabled": True, "remark": "W05 W06 Bus integration trial"})
    cur = call("GET", f"/roles/{ROLE}/agents")
    call("PUT", f"/roles/{ROLE}/agents", {"entries": [{"agentId": a} for a in services], "expectedRevision": cur["revision"]})
    subject = bus["subject"]
    cur = call("GET", f"/users/{subject}/roles")
    roles = [e["roleId"] for e in cur.get("entries", [])]
    record["subject_roles_before"] = roles
    if ROLE not in roles:
        call("PUT", f"/users/{subject}/roles", {"entries": [{"roleId": r} for r in roles + [ROLE]], "expectedRevision": cur["revision"]})
    record["subject_roles_after"] = [e["roleId"] for e in call("GET", f"/users/{subject}/roles").get("entries", [])]
    record["permission_preview"] = call("POST", "/effective-permissions:preview", {"subjectType": "USER", "subjectId": subject})
    (HOME / "grant.json").write_text(json.dumps(record, indent=2, ensure_ascii=False))
    print(json.dumps({"registered": {k: v.get("instanceId") for k, v in regs.items()},
                      "roles_before": record["subject_roles_before"], "roles_after": record["subject_roles_after"]}))


def heartbeat():
    while True:
        regs = json.loads(REG.read_text())
        health = {}
        for sid, row in regs.items():
            st, _ = http("PUT", bus["rdc_url"] + f"/v2/agents/{sid}/instances/{row['instanceId']}/heartbeat", None, registry)
            if st == 404:
                regs[sid] = register_one(sid, services[sid])
                REG.write_text(json.dumps(regs, indent=2))
                st = "re-registered"
            health[sid] = st
        (HOME / "heartbeat-status.json").write_text(json.dumps({"at": time.time(), "agents": health}))
        time.sleep(8)


{"register": register, "heartbeat": heartbeat}[sys.argv[1]]()
