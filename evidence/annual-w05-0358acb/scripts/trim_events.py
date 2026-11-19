import hashlib, json, sys
p = sys.argv[1]
d = json.load(open(p, encoding="utf-8"))
def trim(e):
    raw = json.dumps(e["document"], ensure_ascii=False, sort_keys=True)
    if len(raw) > 1500:
        e = dict(e)
        e["document"] = {"_trimmed_chars": len(raw), "_sha256": hashlib.sha256(raw.encode()).hexdigest(),
                         "node_id": e["document"].get("node_id") if isinstance(e["document"], dict) else None,
                         "status": e["document"].get("status") if isinstance(e["document"], dict) else None}
    return e
for k in ("events_first_1000", "events_last_loop", "events"):
    if k in d:
        d[k] = [trim(e) for e in d[k]]
json.dump(d, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
