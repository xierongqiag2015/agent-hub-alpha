"""Offline check of the model egress recorder's timing (adapted from the
reviewer's counterexample, PR #31 comment 6967911632): the upstream receives
the request and blocks; a request_started line must already be on record
before any response, and the finished line must follow with the same id.
Run: python -I recorder_timing_check.py <path to model_egress.py>"""
import ast
import http.server
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import urllib.request

received, release = threading.Event(), threading.Event()


class Upstream(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        self.rfile.read(int(self.headers["Content-Length"]))
        received.set()
        release.wait(5)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"{}")


up = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
threading.Thread(target=up.serve_forever, daemon=True).start()
os.environ["UPSTREAM"] = f"http://127.0.0.1:{up.server_port}"
tree = ast.parse(Path(sys.argv[1]).read_text())
assert ast.unparse(tree.body[-1]).endswith(".serve_forever()")
tree.body.pop()  # only the standalone server launch is replaced, with an ephemeral port
ns = {"__name__": "checked_egress"}
exec(compile(tree, "<model_egress.py>", "exec"), ns)
with tempfile.TemporaryDirectory() as tmp:
    ns["LOG"] = str(Path(tmp) / "egress.jsonl")
    proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 0), ns["Egress"])
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    errors = []

    def request():
        try:
            req = urllib.request.Request(f"http://127.0.0.1:{proxy.server_port}/chat/completions", data=b'{"model":"model-b"}',
                                         headers={"Content-Type": "application/json"})
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=10) as r:
                r.read()
        except Exception as e:
            errors.append(str(e))

    caller = threading.Thread(target=request)
    caller.start()
    try:
        assert received.wait(3), "fake upstream did not receive request"
        before = [json.loads(l) for l in Path(ns["LOG"]).read_text().splitlines()]
        print(json.dumps({"upstream_received": True, "logged_before_response": len(before),
                          "events": [e["event"] for e in before], "model": before[0]["model"] if before else None,
                          "inflight": before[0].get("inflight") if before else None}))
    finally:
        release.set()
    caller.join(10)
    assert not errors, errors
    after = [json.loads(l) for l in Path(ns["LOG"]).read_text().splitlines()]
    print(json.dumps({"logged_after_response": len(after), "events": [e["event"] for e in after],
                      "same_request_id": len({e["request_id"] for e in after}) == 1}))
    assert len(before) == 1 and before[0]["event"] == "request_started" and before[0]["model"] == "model-b"
    assert [e["event"] for e in after] == ["request_started", "response_finished"]
    proxy.shutdown()
up.shutdown()
print("recorder timing check passed")
