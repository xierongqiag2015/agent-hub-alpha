#!/usr/bin/env python3
"""Test-only model egress recorder (AR-04 M1 evidence).

Sits between the executors and the real OpenAI-compatible model endpoint and
forwards every request unchanged. For each request it appends one line to
/tmp/model-egress.jsonl with the time, caller address, path, the "model" field
of the request body, and the upstream status. It records no prompt, no
response body and no header values; the Authorization header is forwarded,
never stored."""
import http.server
import json
import os
import threading
import time
import urllib.error
import urllib.request

UPSTREAM = os.environ["UPSTREAM"].rstrip("/")
LOG = "/tmp/model-egress.jsonl"
LOCK = threading.Lock()


class Egress(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _forward(self, method):
        data = self.rfile.read(int(self.headers.get("Content-Length", "0") or 0)) if method == "POST" else None
        model = None
        if data:
            try:
                model = json.loads(data).get("model")
            except ValueError:
                model = None
        headers = {k: v for k, v in self.headers.items() if k.lower() in ("authorization", "content-type", "accept")}
        req = urllib.request.Request(UPSTREAM + self.path, data=data, headers=headers, method=method)
        status, body, ctype = 502, b'{"error":"egress upstream unreachable"}', "application/json"
        try:
            with urllib.request.urlopen(req, timeout=300) as resp:
                status, body, ctype = resp.status, resp.read(), resp.headers.get("Content-Type", "application/json")
        except urllib.error.HTTPError as exc:
            status, body, ctype = exc.code, exc.read(), exc.headers.get("Content-Type", "application/json")
        except OSError:
            pass
        with LOCK, open(LOG, "a") as f:
            f.write(json.dumps({"at": time.time(), "caller": self.client_address[0], "method": method,
                                "path": self.path, "model": model, "status": status}) + "\n")
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        self._forward("POST")

    def do_GET(self):
        self._forward("GET")


http.server.ThreadingHTTPServer(("0.0.0.0", 8098), Egress).serve_forever()
