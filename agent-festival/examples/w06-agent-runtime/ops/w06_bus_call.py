"""Call the W06 role agents through the annual environment's Agent Bus Gateway (DIRECT), as the
worker identity the annual orchestrator uses, chaining real outputs: material -> metrics ->
operation | asset-quality -> writer. W05 test driver, not the W03 Graph executor.
Usage: w06_bus_call.py. Evidence: ~/w06-bus/calls-<ts>.json (no tokens)."""
import json
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

ROOT = Path.home() / "report-demo-env/repo/.local/report-demo"
HOME = Path.home() / "w06-bus"
bus = json.loads((ROOT / "config.json").read_text())["bus"]
DONE = ("COMPLETED", "FAILED", "CANCELED", "REJECTED", "INPUT_REQUIRED", "AUTH_REQUIRED")


def post(url, body, headers, form=False):
    data = urllib.parse.urlencode(body).encode() if form else json.dumps(body).encode()
    h = {**headers, "Content-Type": "application/x-www-form-urlencoded" if form else "application/json"}
    req = urllib.request.Request(url, data=data, headers=h, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"text": raw.decode(errors="replace")[:300]}


st, tok = post(bus["token_url"], {"grant_type": "client_credentials", "client_id": bus["client_id"], "client_secret": bus["client_secret"]}, {}, form=True)
assert st == 200, st
H = {"Authorization": "Bearer " + tok["access_token"], "X-User-ID": bus["subject"]}


def state_of(task):
    return str((task.get("status") or {}).get("state", "")).upper().replace("TASK_STATE_", "")


def text_of(task):
    out = []
    for a in task.get("artifacts") or []:
        for p in a.get("parts") or []:
            if p.get("text"):
                out.append(p["text"])
    msg = (task.get("status") or {}).get("message") or {}
    for p in msg.get("parts") or []:
        if p.get("text") and not out:
            out.append(p["text"])
    return "\n".join(out)


def call(agent, prompt, timeout=480):
    rec = {"agent": agent, "request_id": str(uuid.uuid4()), "message_id": "message-" + uuid.uuid4().hex, "context_id": "context-" + uuid.uuid4().hex,
           "prompt_chars": len(prompt), "sent_at": time.time()}
    st, resp = post(bus["gateway_url"] + "/a2a", {"jsonrpc": "2.0", "id": rec["request_id"], "method": "SendMessage", "params": {
        "message": {"role": "ROLE_USER", "messageId": rec["message_id"], "contextId": rec["context_id"], "parts": [{"kind": "text", "text": prompt}]},
        "configuration": {"returnImmediately": True}, "metadata": {"agentId": agent}}}, H)
    rec["send_http"] = st
    task = (resp.get("result") or {}).get("task", resp.get("result")) if isinstance(resp, dict) else None
    if st != 200 or not isinstance(task, dict) or not task.get("id"):
        rec["send_error"] = {k: resp.get(k) for k in ("error", "code", "text") if isinstance(resp, dict) and k in resp}
        print(json.dumps({"agent": agent, "send_http": st, "error": rec.get("send_error")}, ensure_ascii=False)[:400], flush=True)
        return rec, ""
    rec.update(remote_task_id=task["id"], remote_context_id=task.get("contextId"), polls=0)
    deadline = time.monotonic() + timeout
    while state_of(task) not in DONE and time.monotonic() < deadline:
        time.sleep(2)
        st, resp = post(bus["gateway_url"] + "/a2a", {"jsonrpc": "2.0", "id": str(uuid.uuid4()), "method": "GetTask", "params": {"id": rec["remote_task_id"]}}, H)
        rec["polls"] += 1
        if st == 200 and isinstance(resp.get("result"), dict):
            task = resp["result"].get("task", resp["result"])
    rec.update(state=state_of(task), finished_at=time.time())
    rec["seconds"] = round(rec["finished_at"] - rec["sent_at"], 1)
    output = text_of(task)
    rec["output"] = output
    print(json.dumps({"agent": agent, "task": rec["remote_task_id"], "state": rec["state"], "seconds": rec["seconds"], "polls": rec["polls"],
                      "output_head": output[:160]}, ensure_ascii=False), flush=True)
    return rec, output


calls = []
r, material = call("w06-material", "请为招商银行股份有限公司 2025 年报（本集团口径，2025 对比 2024）的八项固定指标取证：先用 list_metrics_index 找到各指标所在页，再用 read_page 读取原文，"
                                   "按你的输出契约逐项返回 digest、page、excerpt。快照只有第 14—16 页。")
calls.append(r)
r, metrics = call("w06-metrics", "以下是资料 agent 从固定年报快照取得的原文片段（含 digest、page、excerpt）。请按你的输出契约抽取八项固定指标：\n" + material)
calls.append(r)
r, operation = call("w06-operation", "以下是指标抽取 agent 的结构化结果。请完成经营分析（营业收入、归母净利润、净息差、成本收入比）：\n" + metrics)
calls.append(r)
r, quality = call("w06-asset-quality", "以下是指标抽取 agent 的结构化结果。请完成资产质量分析（不良贷款余额、不良贷款率、拨备覆盖率、核心一级资本充足率）：\n" + metrics)
calls.append(r)
TASK_ID = "w06-bus-" + uuid.uuid4().hex[:8]
r, report = call("w06-writer", "按你的输出契约合成年报工件。\n"
                 f"task_id：{TASK_ID}\ncompany：招商银行股份有限公司（本集团口径）\nreport_period：2025 年（对比 2024 年）\n"
                 "【八指标结构化 artifact（指标抽取 agent 输出，原样转抄）】\n" + metrics
                 + "\n【经营分析草稿（已提交，带引用）】\n" + operation + "\n【资产质量分析草稿（已提交，带引用）】\n" + quality)
calls.append(r)
out = HOME / f"calls-{int(time.time())}.json"
out.write_text(json.dumps({"gateway": bus["gateway_url"], "mode": "DIRECT", "identity": "annual worker client (same as orchestrator)",
                           "driver": "W05 test driver chaining outputs; not the W03 Graph executor", "calls": calls}, ensure_ascii=False, indent=2))
print("Evidence:", out)
