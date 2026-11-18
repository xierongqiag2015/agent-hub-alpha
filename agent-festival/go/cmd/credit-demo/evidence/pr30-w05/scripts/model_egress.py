#!/usr/bin/env python3
"""Test-only model egress recorder (AR-04 M1 evidence).

Sits between the executors and the real OpenAI-compatible model endpoint and
forwards every request unchanged. Each request is written twice to
/tmp/model-egress.jsonl, sharing a request_id:
  - "request_started" BEFORE the request is forwarded (so a request that never
    finishes is still on record), with the caller, path, the request's "model"
    field and the number of requests in flight;
  - "response_finished" or "error" when the upstream answer (or failure) is in,
    with status and duration.
It records no prompt, no response body and no header values; the
Authorization header is forwarded, never stored. Each line is flushed and
fsynced before the request proceeds."""
import http.server
import json
import os
import threading
import time
import urllib.error
import urllib.request
import uuid

UPSTREAM = os.environ["UPSTREAM"].rstrip("/")
LOG = "/tmp/model-egress.jsonl"
LOCK = threading.Lock()
INFLIGHT = 0


def record(entry):
    with LOCK, open(LOG, "a") as f:
        f.write(json.dumps(entry) + "\n")
        f.flush()
        os.fsync(f.fileno())


class Egress(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _forward(self, method):
        global INFLIGHT
        data = self.rfile.read(int(self.headers.get("Content-Length", "0") or 0)) if method == "POST" else None
        model = None
        if data:
            try:
                model = json.loads(data).get("model")
            except ValueError:
                model = None
        rid = uuid.uuid4().hex
        started = time.time()
        with LOCK:
            INFLIGHT += 1
            inflight = INFLIGHT
        record({"event": "request_started", "request_id": rid, "at": started, "caller": self.client_address[0],
                "method": method, "path": self.path, "model": model, "inflight": inflight})
        headers = {k: v for k, v in self.headers.items() if k.lower() in ("authorization", "content-type", "accept")}
        req = urllib.request.Request(UPSTREAM + self.path, data=data, headers=headers, method=method)
        status, body, ctype, error = 502, b'{"error":"egress upstream unreachable"}', "application/json", None
        try:
            with urllib.request.urlopen(req, timeout=300) as resp:
                status, body, ctype = resp.status, resp.read(), resp.headers.get("Content-Type", "application/json")
        except urllib.error.HTTPError as exc:
            status, body, ctype = exc.code, exc.read(), exc.headers.get("Content-Type", "application/json")
        except OSError as exc:
            error = type(exc).__name__
        finally:
            with LOCK:
                INFLIGHT -= 1
        record({"event": "error" if error else "response_finished", "request_id": rid, "at": time.time(),
                "status": None if error else status, "error": error, "duration": round(time.time() - started, 3)})
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
