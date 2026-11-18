"""Collect W05's single-side annual D1 record on 0358acb (read-only API calls),
then scan every output file for secret values (names printed, never values)."""
import hashlib, json, os, pathlib, subprocess, sys, urllib.request
home = pathlib.Path.home()
repo = home / "report-demo-env/repo"
root = repo / ".local/report-demo"
cfg = json.loads((root / "config.json").read_text())
out = pathlib.Path(sys.argv[1]); out.mkdir(parents=True, exist_ok=True)
op = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def get(p):
    r = urllib.request.Request(cfg["public_url"] + p, headers={"Authorization": "Bearer " + cfg["tokens"]["operator"]})
    return json.load(op.open(r, timeout=15))
tasks = {"T-01858b3e91f6a6c15f51ac96158ecb2e": "0358acb", "T-0949586ac6722295125f7aa057fb753f": "8c31bcd", "T-20ef410acff24a0aab3afb19297656d8": "8c31bcd"}
for tid, ver in tasks.items():
    rec = {"task_id": tid, "source": ver, "task": get("/v1/tasks/" + tid), "calls": get(f"/v1/tasks/{tid}/calls"),
           "verify": get(f"/v1/tasks/{tid}/verify"), "workbench": get("/v1/workbench/tasks/" + tid)}
    (out / f"{tid[:10]}-{ver}.json").write_text(json.dumps(rec, ensure_ascii=False, indent=1), encoding="utf-8")
sh = lambda *a: subprocess.run(a, capture_output=True, text=True, cwd=repo).stdout.strip()
env = {"repo_head": sh("git", "rev-parse", "HEAD"),
       "sources": {d.name: sh("git", "-C", str(d), "rev-parse", "HEAD") for d in (repo / ".local/sources").iterdir() if (d / ".git").exists()},
       "artifacts": {p: hashlib.sha256((repo / p).read_bytes()).hexdigest() for p in (
           "demos/report-demo/orchestrator-java/target/report-orchestrator.jar", "demos/report-demo/experts-java/target/report-experts.jar",
           ".local/report-demo/bin/report-control")},
       "execution_profile": cfg.get("execution_profile"), "model_binding_digest": cfg.get("model_binding_digest"),
       "health": get("/healthz") if False else None}
(out / "environment.json").write_text(json.dumps(env, ensure_ascii=False, indent=1), encoding="utf-8")
# secret values: report-demo config tokens/passwords, secrets dir, swarm secrets
vals = {}
def walk(o, p=""):
    if isinstance(o, dict):
        for k, v in o.items(): walk(v, p + k + ".")
    elif isinstance(o, list):
        for i, v in enumerate(o): walk(v, p + str(i) + ".")
    elif isinstance(o, str) and len(o) >= 8 and any(x in p.lower() for x in ("token", "password", "secret", "key")):
        vals["config." + p[:-1]] = o
walk(cfg)
for f in (home / "report-demo-env/secrets").iterdir():
    vals["secrets/" + f.name] = f.read_text().strip()
for line in (home / ".config/aha-swarm/secrets.env").read_text().splitlines():
    if "=" in line and not line.startswith("#"):
        k, v = line.split("=", 1)
        if len(v) >= 8 and k not in ("SWARM_MODEL_NAME", "SWARM_API_BASE"): vals["swarm." + k] = v
for exp in (root / "experts").glob("*.json"):
    walk(json.loads(exp.read_text()), "expert." + exp.stem + ".")
bad = 0
for f in sorted(out.iterdir()):
    text = f.read_text(encoding="utf-8", errors="replace")
    found = [k for k, v in vals.items() if v and v in text]
    bad += bool(found)
    print(f.name, f.stat().st_size, "LEAK " + ",".join(found) if found else "clean")
print("scanned", len(vals), "secret values;", "no leaks" if not bad else "LEAKS FOUND")
