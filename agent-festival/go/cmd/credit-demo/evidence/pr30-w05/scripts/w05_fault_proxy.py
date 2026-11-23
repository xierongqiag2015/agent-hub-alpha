#!/usr/bin/env python3
"""Test-only transport faults in front of the real Gateway; never mocks Agents.

W05 evidence copy of deploy/bus/fault_proxy.py (41a3675) with added modes:
  drop-send-reply-once: the next SendMessage is forwarded to the real Gateway
    (so the remote task is created) but the caller receives 502 instead of the
    receipt.
  mask-next-send / mask-task: the next SendMessage is forwarded and answered
    normally; its remote task id is kept in /tmp/bus-mask-task and the mode
    becomes mask-task. While mask-task holds, every reply about that task is
    forwarded from the real Gateway but a final state (COMPLETED, FAILED, ...)
    is shown to the caller as TASK_STATE_WORKING without artifacts: the remote
    accepted the call and the caller sees no final result. Other tasks pass.
    Writing "pass" to the mode file releases it; the next query then returns
    the real Gateway reply unchanged.
All original modes behave exactly as in the repository copy. Mounted over the
original path by compose.w05-send-test.yaml; product code unchanged."""
import hashlib
import http.server
import json
import os
from pathlib import Path
import threading
import time
import urllib.error
import urllib.request

LOCK = threading.Lock()
MODE = Path("/tmp/bus-fault-mode")
EVENTS = Path("/tmp/bus-fault-events.jsonl")
MASK_TASK = Path("/tmp/bus-mask-task")
MASKING = ("mask-next-send", "mask-task")


def reply_task(response):
    try:
        result = json.loads(response).get("result", {})
        return result, result.get("task", result)
    except (ValueError, AttributeError):
        return None, None


def artifact_digest(task):
    """Digest of the artifact's output as Go's CommitCall records it (sha256 of the text and a NUL)."""
    text = "".join(p.get("text", "") for a in task.get("artifacts", []) for p in a.get("parts", []))
    try:
        output = json.loads(text).get("output", "")
    except (ValueError, AttributeError):
        return None
    return "sha256:" + hashlib.sha256(output.encode() + b"\0").hexdigest()


class Proxy(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        data = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        body = json.loads(data)
        method = body.get("method")
        with LOCK:
            mode = MODE.read_text().strip() if MODE.exists() else "pass"
            masking = mode if mode in MASKING else None
            if masking:
                mode = "pass"  # forwarded unchanged; only the reply is masked below
            elif method == "SendMessage" and mode == "drop-send-reply-once":
                MODE.write_text("pass")
            elif method == "GetTask" and mode not in ("pass", "hold-completed-once", "drop-send-reply-once"):
                MODE.write_text("pass")
            elif method != "GetTask" or mode == "drop-send-reply-once":
                mode = "pass"
        status = 200
        if mode == "drop-send-reply-once":
            headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection", "content-length")}
            req = urllib.request.Request(os.environ.get("UPSTREAM", "http://gateway:8080") + self.path, data=data, headers=headers)
            try:
                with urllib.request.urlopen(req, timeout=60) as upstream:
                    upstream_status, upstream_body = upstream.status, upstream.read()
            except urllib.error.HTTPError as exc:
                upstream_status, upstream_body = exc.code, exc.read()
            except OSError:
                upstream_status, upstream_body = 0, b""
            try:
                forwarded_task = json.loads(upstream_body).get("result", {})
                forwarded_task = forwarded_task.get("task", forwarded_task).get("id")
            except (ValueError, AttributeError):
                forwarded_task = None
            status = 502
            response = json.dumps({"code": "TEST_SEND_REPLY_LOST", "message": "Injected lost SendMessage reply"}).encode()
            body.setdefault("params", {})["w05_forwarded"] = {"upstream_status": upstream_status, "remote_task_id": forwarded_task}
        elif mode == "drop-query-once":
            status = 503
            response = json.dumps({"code": "TEST_CONNECTION_LOST", "message": "Injected lost query response"}).encode()
        else:
            headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection", "content-length")}
            req = urllib.request.Request(os.environ.get("UPSTREAM", "http://gateway:8080") + self.path, data=data, headers=headers)
            try:
                with urllib.request.urlopen(req, timeout=60) as upstream:
                    status, response = upstream.status, upstream.read()
            except urllib.error.HTTPError as exc:
                status, response = exc.code, exc.read()
            except OSError:
                status, response = 502, b'{"code":"TEST_UPSTREAM_DISCONNECTED"}'
        if mode == "hold-completed-once":
            result = json.loads(response).get("result", {})
            task = result.get("task", result)
            if task.get("status", {}).get("state") == "TASK_STATE_COMPLETED":
                with LOCK:
                    MODE.write_text("pass")
                    Path("/tmp/bus-completed-ready").write_text("ready")
                deadline = time.monotonic() + 55
                while not Path("/tmp/bus-release").exists() and time.monotonic() < deadline:
                    time.sleep(0.1)
            else:
                mode = "pass"
        mask = None
        if masking and status == 200:
            result, task = reply_task(response)
            with LOCK:
                if masking == "mask-next-send" and method == "SendMessage" and task and task.get("id") \
                        and MODE.read_text().strip() == "mask-next-send":
                    MASK_TASK.write_text(task["id"])
                    MODE.write_text("mask-task")
                    masking = "mask-task"
                target = MASK_TASK.read_text().strip() if MASK_TASK.exists() else ""
            if masking == "mask-task" and task and task.get("id") == target:
                state = task.get("status", {}).get("state")
                mask = {"task": target, "upstream_state": state, "masked": False}
                if task.get("artifacts"):
                    mask["upstream_artifact_digest"] = artifact_digest(task)
                if state not in ("TASK_STATE_SUBMITTED", "TASK_STATE_WORKING"):
                    task["status"] = {**task.get("status", {}), "state": "TASK_STATE_WORKING"}
                    task.pop("artifacts", None)
                    reply = json.loads(response)
                    reply["result"] = result
                    response = json.dumps(reply).encode()
                    mask["masked"] = True
        event = {"at": time.time(), "method": method, "id": body.get("id"),
                 "task_id": body.get("params", {}).get("id"), "status": status, "fault": masking or mode}
        if mask:
            event["mask"] = mask
        if "w05_forwarded" in body.get("params", {}):
            event["forwarded"] = body["params"]["w05_forwarded"]
        with LOCK, EVENTS.open("a") as stream:
            stream.write(json.dumps(event) + "\n")
        if mode == "late-query-once":
            time.sleep(35)  # Exceeds the caller's 30-second deadline; not a model delay.
        try:
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(response)))
            self.end_headers()
            self.wfile.write(response)
        except (BrokenPipeError, ConnectionResetError):
            pass


http.server.ThreadingHTTPServer(("0.0.0.0", 8099), Proxy).serve_forever()
