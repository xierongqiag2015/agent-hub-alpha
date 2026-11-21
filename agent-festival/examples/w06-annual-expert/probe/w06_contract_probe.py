"""Contract probe: replay the exact business-layer messages of the delivered annual task
T-824c5bf9 (bindings from its NODE_RESOLVED audit events, material from the annual
environment) into the W06 agents and, as a baseline, the W03/W04 role agents, through
the platform's JiuwenCoreAgentHandler path; check each output with the platform's
parseOutput and the annual role contracts. No control service, Gateway or task is touched.
Usage: w06_contract_probe.py <out dir>"""
import json
import os
import subprocess
import sys
from pathlib import Path

OUT = Path(sys.argv[1]); OUT.mkdir(parents=True, exist_ok=True)
JAR = os.environ.get("W06_EXPERT_JAR", "/mnt/d/agent-hub-alpha/_deploy175/w06/out/w06-annual-expert.jar")
JAVA = "/usr/lib/jvm/java-21-openjdk-amd64/bin/java"
PRIV = Path.home() / "report-demo-env/repo/.local/report-demo"
cfg = json.loads((PRIV / "config.json").read_text())
material = json.loads((PRIV / "material.json").read_text())
events = json.loads(Path("/mnt/d/agent-hub-alpha/_deploy175/annual-d2/plan-4004/doc-events-all.json").read_text())
binding = {e["document"]["binding"]["node_id"]: e["document"]["binding"] for e in events if e["kind"] == "NODE_RESOLVED"}
ROLES = {"extract": ("extract", "report-extractor"), "business": ("business", "business-analyst"), "write": ("draft0", "report-writer")}
IDS = {"revenue", "net_profit_attributable", "net_interest_margin", "cost_to_income", "npl_balance", "npl_ratio", "provision_coverage", "cet1_ratio_advanced"}
secrets = dict(l.split("=", 1) for l in (Path.home() / ".config/aha-swarm/secrets.env").read_text().splitlines() if "=" in l and not l.startswith("#"))
env = {k: v for k, v in os.environ.items() if k.lower() not in ("http_proxy", "https_proxy")}
env.update(DEMO_MODEL_API_KEY=secrets["SWARM_API_KEY"].strip(), DEMO_MODEL_BASE_URL=secrets["SWARM_API_BASE"].strip(),
           DEMO_MODEL_NAME="deepseek-v4-1-flash-260910")
probe_cfg = OUT / "probe-config.json"
probe_cfg.write_text(json.dumps({"model_binding_digest": cfg["model_binding_digest"]}))


def message(role, node):
    b = dict(binding[node])
    if role == "extract":  # BoundExpertHandler.prepareTask: drop these fields and hand over the full material
        for k in ("task_id", "graph_revision", "node_id", "goal", "focus"):
            b.pop(k, None)
        return {"binding": b, "material": material}
    return {"binding": b, "material": {"id": material["id"], "digest": material["digest"]}}


def indicators_ok(items):
    if not isinstance(items, list):
        return "indicators not a list"
    ids = {i.get("id") for i in items if isinstance(i, dict)}
    if ids != IDS:
        return f"indicator ids differ: missing {sorted(IDS - ids)} extra {sorted(ids - IDS)[:4]}"
    for i in items:
        v, c, ch = i.get("values") or {}, i.get("citation") or {}, i.get("change") or {}
        if not all(isinstance(v.get(y), (int, float)) for y in ("2025", "2024")):
            return f"{i['id']}: values 2025/2024 not both numbers"
        if not (isinstance(c.get("pdf_page"), int) and c.get("printed_page") is not None and c.get("quote")):
            return f"{i['id']}: citation pdf_page/printed_page/quote missing"
        if ch.get("kind") not in ("pct", "pp") or not isinstance(ch.get("value"), (int, float)):
            return f"{i['id']}: change kind/value missing"
    return "ok"


def contract(role, out):
    if not isinstance(out, dict):
        return {"contract": "no JSON object"}
    if role == "extract":
        miss = [k for k in ("snapshot", "scope", "indicators", "limitations") if k not in out]
        return {"missing_keys": miss, "indicators": indicators_ok(out.get("indicators"))}
    if role == "business":
        miss = [k for k in ("section_id", "indicators", "findings", "limitations") if k not in out]
        f = out.get("findings")
        return {"missing_keys": miss, "section_id": out.get("section_id"), "indicators": indicators_ok(out.get("indicators")),
                "findings_shape": isinstance(f, list) and all(isinstance(x, dict) and x.get("text") and isinstance(x.get("evidence"), list) for x in f)}
    miss = [k for k in ("snapshot", "scope", "indicators", "sections", "limitations") if k not in out]
    secs = {s.get("id") for s in out.get("sections", []) if isinstance(s, dict)}
    return {"missing_keys": miss, "indicators": indicators_ok(out.get("indicators")), "section_ids": sorted(x for x in secs if x),
            "sections_ok": {"operating", "asset_quality", "risks"} <= secs}


summary = {}
for role, (node, agent_id) in ROLES.items():
    msg = OUT / f"message-{role}.json"
    msg.write_text(json.dumps(message(role, node), ensure_ascii=False))
    for mode in ("baseline", "w06"):
        res = OUT / f"{mode}-{role}.json"
        subprocess.run([JAVA, "-Xmx512m", f"-Dloader.main=com.openjiuwen.demo.expert.W06ContractProbe", "-cp", JAR,
                        "org.springframework.boot.loader.launch.PropertiesLauncher", mode, role, agent_id, str(probe_cfg), str(msg), str(res)],
                       env=env, stdout=open(OUT / f"{mode}-{role}.log", "w"), stderr=subprocess.STDOUT, timeout=900)
        r = json.loads(res.read_text()) if res.exists() else {"error": "no result file"}
        row = {"seconds": r.get("seconds"), "platform_parse": r.get("platform_parse"), "error": r.get("error")}
        if str(r.get("platform_parse", "")).startswith("accepted"):
            row["contract"] = contract(role, r.get("output"))
        else:
            row["raw_head"] = (r.get("raw_output") or "")[:200]
        summary[f"{role}/{mode}"] = row
        print(json.dumps({f"{role}/{mode}": row}, ensure_ascii=False)[:700], flush=True)
(OUT / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=1))
