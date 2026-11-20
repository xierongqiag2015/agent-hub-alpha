"""Check a W06 Bus chain run: writer copied the metrics verbatim (W06 rule 1), current-year values
vs W01 answers, Gateway audit counts in the run window, RDC instances and process identities.
Usage: w06_check_run.py <calls-*.json> <window start ISO> <window end ISO>"""
import json
import re
import sys
import urllib.request
from pathlib import Path

run = json.loads(Path(sys.argv[1]).read_text())
start, end = sys.argv[2], sys.argv[3]
calls = {c["agent"]: c for c in run["calls"]}
result = {"states": {a: c.get("state") for a, c in calls.items()}, "seconds": {a: c.get("seconds") for a, c in calls.items()}}


def first_json(text):
    text = text.strip()
    m = re.search(r"```(?:json)?\s*(.*?)```", text, re.S)
    if m:
        text = m.group(1)
    i = min([k for k in (text.find("["), text.find("{")) if k >= 0])
    return json.loads(text[i:])


metrics = first_json(calls["w06-metrics"]["output"])
writer = first_json(calls["w06-writer"]["output"])
result["writer_keys"] = sorted(writer)
result["writer_metrics_verbatim"] = writer.get("metrics") == metrics
result["writer_sections"] = [s.get("title") for s in writer.get("sections", [])]
result["writer_citations"] = len(writer.get("citations", []))
answers = json.loads((Path.home() / "report-demo-env/repo/demo/annual-report/answers.json").read_text())
items = answers["indicators"] if isinstance(answers["indicators"], list) else list(answers["indicators"].values())
KEY = {"revenue": "OPERATING_REVENUE", "net_profit_attributable": "NET_PROFIT_ATTRIBUTABLE", "net_interest_margin": "NET_INTEREST_MARGIN",
       "cost_to_income": "COST_INCOME_RATIO", "npl_balance": "NPL_BALANCE", "npl_ratio": "NPL_RATIO", "provision_coverage": "PROVISION_COVERAGE",
       "cet1_ratio_advanced": "CET1_RATIO"}
ref = {KEY[i["id"]]: i["values"]["2025"] for i in items if i.get("id") in KEY}
got = {m["key"]: m.get("value") for m in metrics}
num = lambda v: float(str(v).replace(",", "").replace("%", "")) if v not in (None, "NOT_FOUND") else None
result["metrics_vs_w01_2025"] = {k: {"w06": got.get(k), "w01": ref[k], "match": num(got.get(k)) == float(ref[k])} for k in ref}
result["metrics_all_match"] = all(v["match"] for v in result["metrics_vs_w01_2025"].values())
log = Path.home() / "report-demo-env/repo/.local/report-demo/logs/gateway.out.log"
lines = [l for l in log.read_text().splitlines() if "LoggingAuditSink" in l and start <= l[:29] <= end]
counts = {}
for l in lines:
    m = re.search(r"method=(\w+) outcome=(\w+)", l)
    if m:
        counts[m.group(0)] = counts.get(m.group(0), 0) + 1
result["gateway_audit_window"] = {"from": start, "to": end, "counts": counts,
                                  "send_times": [l[:29] for l in lines if "method=SendMessage" in l]}
cfg = json.loads((Path.home() / "report-demo-env/repo/.local/report-demo/config.json").read_text())["bus"]
services = json.loads((Path.home() / "w06-bus/services.json").read_text())
inst = {}
for sid, rec in services.items():
    req = urllib.request.Request(cfg["rdc_url"] + f"/v2/agents/{sid}/instances", headers={"Authorization": "Bearer " + cfg["registry_token"], "X-Tenant-ID": cfg["tenant"]})
    data = json.loads(urllib.request.urlopen(req, timeout=10).read())
    rows = data.get("instances", data)
    stat = Path(f"/proc/{rec['pid']}/stat").read_text().split()
    inst[sid] = {"rdc_instances": [(r.get("instanceId"), r.get("health")) for r in rows], "pid": rec["pid"], "port": rec["port"],
                 "start_ticks_recorded": rec["start_ticks"], "start_ticks_live": int(stat[21]), "boot_id": rec["boot_id"]}
result["instances"] = inst
print(json.dumps(result, ensure_ascii=False, indent=1))
Path(sys.argv[1]).with_name(Path(sys.argv[1]).stem + "-check.json").write_text(json.dumps(result, ensure_ascii=False, indent=1))
