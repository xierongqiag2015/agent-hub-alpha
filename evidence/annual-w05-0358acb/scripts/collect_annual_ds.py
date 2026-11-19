"""Collect the deepseek-run records (read-only API calls) for the annual D1
evidence: task, calls, verify, workbench and audit events. The deadlocked task
has ~15k events; it is summarised (kind counts, first 1000, the last loop and
every human/pause/skip event) instead of copied whole."""
import collections, hashlib, json, pathlib, subprocess, sys, urllib.request
home = pathlib.Path.home()
repo = home / "report-demo-env/repo"
root = repo / ".local/report-demo"
cfg = json.loads((root / "config.json").read_text())
out = pathlib.Path(sys.argv[1]); out.mkdir(parents=True, exist_ok=True)
op = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def get(p):
    r = urllib.request.Request(cfg["public_url"] + p, headers={"Authorization": "Bearer " + cfg["tokens"]["operator"]})
    return json.load(op.open(r, timeout=60))
def events(tid):
    allev, after = [], 0
    while True:
        page = get(f"/v1/tasks/{tid}/events?after={after}")
        if not page: break
        allev += page; after = int(page[-1]["sequence"])
        if len(page) < 200: break
    return allev
for tid, tag in (("T-bdc4aa18d8f8e0b979a7d0e628dc2e94", "deepseek-human-deadlock"), ("T-8fae6cb5b646d334defc623e6e42e469", "deepseek-delivered")):
    rec = {"task_id": tid, "source": "0358acb", "model": "deepseek-v4-1-flash-260910", "task": get("/v1/tasks/" + tid),
           "calls": get(f"/v1/tasks/{tid}/calls"), "verify": get(f"/v1/tasks/{tid}/verify"), "workbench": get("/v1/workbench/tasks/" + tid)}
    ev = events(tid)
    rec["event_kinds"] = dict(collections.Counter(e["kind"] for e in ev))
    rec["event_count"] = len(ev)
    if len(ev) > 2000:
        keep = {"HUMAN_REVIEW_RECORDED", "PAUSE_CHANGED", "SKIP_AUTHORIZED"}
        rec["events_first_1000"] = ev[:1000]
        rec["events_human_pause_skip"] = [e for e in ev if e["kind"] in keep][:40]
        rec["events_last_loop"] = ev[-20:]
    else:
        rec["events"] = ev
    (out / f"{tid[:10]}-{tag}.json").write_text(json.dumps(rec, ensure_ascii=False, indent=1), encoding="utf-8")
    print(tid, tag, "events", len(ev))
sh = lambda *a: subprocess.run(a, capture_output=True, text=True, cwd=repo).stdout.strip()
env = {"repo_head": sh("git", "rev-parse", "HEAD"), "model": "deepseek-v4-1-flash-260910", "endpoint_profile": "openai",
       "execution_profile": cfg.get("execution_profile"), "model_binding_digest": cfg.get("model_binding_digest"),
       "artifacts": {p: hashlib.sha256((repo / p).read_bytes()).hexdigest() for p in (
           "demos/report-demo/orchestrator-java/target/report-orchestrator.jar", "demos/report-demo/experts-java/target/report-experts.jar",
           ".local/report-demo/bin/report-control")}}
(out / "environment-deepseek.json").write_text(json.dumps(env, ensure_ascii=False, indent=1), encoding="utf-8")
smoke = json.loads((root / "evidence/platform-live-smoke.json").read_text())
(out / "platform-live-smoke-T-8fae6cb5.json").write_text(json.dumps(smoke, ensure_ascii=False, indent=2), encoding="utf-8")
