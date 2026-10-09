"""Observable external work for the public run-control acceptance journey."""
import hashlib
import json
import re
import select
import socket
import threading
import time
import uuid

LOCK = threading.Lock()
JOBS = {}
GATES = {}


def arm(body):
    token = body["token"]
    with LOCK:
        if body.get("kind") in ("model", "tool"):
            GATES[token] = {"kind": body["kind"], "release": threading.Event(),
                            "state": "armed", "ticks": 0, "calls": 0}
        else:
            JOBS[token] = {"mode": body.get("mode", "confirm"), "request": None,
                           "stop": threading.Event(), "finish": threading.Event(), "confirm": threading.Event(), "reject": threading.Event(),
                           "state": "armed", "ticks": 0, "sequence": 0, "updates": [],
                           "stop_requests": [], "texts": [], "digest": "", "bot_token": body.get("bot_token", "")}
    return {"ok": True}


def snapshot(token):
    with LOCK:
        item = JOBS.get(token) or GATES.get(token)
        if item is None:
            return {"state": "missing"}
        return {key: value for key, value in item.items()
                if key not in ("release", "stop", "finish", "confirm", "reject", "request", "bot_token")}


def command(path, body):
    token = body["token"]
    if path == "/run-control/arm":
        return arm(body)
    if path == "/run-control/observe":
        return snapshot(token)
    with LOCK:
        item = JOBS.get(token) or GATES.get(token)
        if item is None:
            return {"ok": False}
        event = (item.get("release") if path == "/run-control/release" else
                 item.get("confirm") if path == "/run-control/confirm" else
                 item.get("reject") if path == "/run-control/reject" else item.get("finish"))
        if event is None:
            return {"ok": False}
        event.set()
    return {"ok": True}


def disconnected(handler):
    readable, _, _ = select.select([handler.connection], [], [], .02)
    if not readable:
        return False
    try:
        return handler.connection.recv(1, socket.MSG_PEEK) == b""
    except (ConnectionError, OSError):
        return True


def held(token, kind, handler):
    with LOCK:
        gate = GATES.get(token)
        if gate is None or gate["kind"] != kind:
            return False
        gate["calls"] += 1
        gate["state"] = "running"
    deadline = time.monotonic() + 90
    digest = hashlib.sha256(token.encode()).digest()
    while not gate["release"].is_set() and time.monotonic() < deadline:
        if disconnected(handler):
            with LOCK:
                gate["state"] = "cancelled"
            return True
        digest = hashlib.sha256(digest).digest()
        with LOCK:
            gate["ticks"] += 1
    with LOCK:
        gate["state"] = "released" if gate["release"].is_set() else "timed_out"
    return False


def model(body, handler, text):
    if not body.get("model", "").startswith("fixture-control-"):
        return False
    messages = body.get("messages") or []
    inputs = [item["content"].split("\n", 1)[1] for item in messages
              if item.get("role") == "user" and isinstance(item.get("content"), str)
              and item["content"].startswith("Accepted work input (not authorization):\n")]
    original = json.loads(inputs[0])["content"] if inputs else text
    token = original.split()[0]
    tools = [item for item in messages if item.get("role") == "tool"]
    streaming = bool(body.get("stream"))
    if streaming:
        handler.send_response(200)
        handler.send_header("Content-Type", "text/event-stream")
        handler.send_header("Connection", "close")
        handler.end_headers()
        handler.wfile.write(b": fixture model computing\n\n")
        handler.wfile.flush()
    if token.startswith("CONTROL_MODEL_") and not tools and held(token, "model", handler):
        handler.close_connection = True
        return True
    if not tools:
        echo = next(item["function"]["name"] for item in body.get("tools", [])
                    if "fixture_echo" in item.get("function", {}).get("name", ""))
        message = {"role": "assistant", "content": None, "tool_calls": [{
            "id": "fixture-call", "type": "function", "function": {
                "name": echo, "arguments": json.dumps({"text": original})}}]}
        reason = "tool_calls"
    else:
        amounts = [int(value) for raw in inputs for value in re.findall(r"\bVALUE=(-?\d+)\b", json.loads(raw)["content"])]
        message = {"role": "assistant", "content": json.dumps({"sum": sum(amounts), "source": "shared accepted inputs"})}
        reason = "stop"
    if streaming:
        delta = ({"role": "assistant", "tool_calls": [dict(message["tool_calls"][0], index=0)]}
                 if reason == "tool_calls" else {"role": "assistant", "content": message["content"]})
        for current in (delta, {}):
            chunk = {"id": "control-model", "object": "chat.completion.chunk", "model": body["model"],
                     "choices": [{"index": 0, "delta": current, "finish_reason": reason if not current else None}]}
            if not current:
                chunk["usage"] = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
            handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
            handler.wfile.flush()
        handler.wfile.write(b"data: [DONE]\n\n")
        handler.wfile.flush()
        handler.close_connection = True
    else:
        handler._json(200, {"id": "control-model", "object": "chat.completion", "model": body["model"],
                            "choices": [{"index": 0, "message": message, "finish_reason": reason}],
                            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
    return True


def tool(body, handler):
    text = body.get("params", {}).get("arguments", {}).get("text", "")
    if not isinstance(text, str) or not text.startswith("CONTROL_TOOL_"):
        return False
    return held(text.split()[0], "tool", handler)


def accept(request, update):
    texts = [item.get("content", "") for item in request.get("inputs", [])]
    with LOCK:
        token = next((key for key in JOBS if texts and texts[0].startswith(key)), None)
        # Security stop deliveries intentionally omit inputs. Route these by the
        # already received run identity without requiring any sensitive material.
        if token is None:
            token = next((key for key, job in JOBS.items()
                          if job["request"] and job["request"]["run"]["id"] == request.get("run_id", request.get("run", {}).get("id"))), None)
        if token is None:
            return
        job = JOBS[token]
        if request.get("operation") == "stop" or request.get("run", {}).get("status") == "stopping":
            job["stop_requests"].append(request)
            job["request"] = dict(job["request"], generation=request["generation"], input_version=request["input_version"], operation="stop")
            job["stop"].set()
            return
        if job["request"] is not None:
            job["request"] = request
            job["texts"] = texts
            return
        job["request"] = request
        job["texts"] = texts
    threading.Thread(target=work, args=(token, update), daemon=True).start()


def send(job, update, status, kind="run.update", result=None):
    with LOCK:
        request = job["request"]
        if kind == "run.update":
            job["sequence"] += 1
        payload = {"type": kind, "run_id": request["run"]["id"],
                   "generation": request["generation"], "input_version": request["input_version"],
                   "sequence": job["sequence"] if kind == "run.update" else 0,
                   "update_key": str(uuid.uuid4()), "status": status}
        if kind == "run.delivery_ack":
            payload["operation"] = request.get("operation", "execute")
        if result is not None:
            payload.update({"result_text": result, "msg_type": "text", "content": {"text": result}})
    try:
        body = {"bot_id": request["run"]["bot_id"], "update": payload}
        if status in ("stopped", "stop_rejected") and job["bot_token"]:
            body["bot_token"] = job["bot_token"]
        response = update(body)
    except (KeyError, ValueError, OSError) as error:
        response = {"status": 0, "error": type(error).__name__}
    with LOCK:
        job["updates"].append({"payload": payload, "response": response})
    return response.get("status", 0) == 200


def work(token, update):
    with LOCK:
        job = JOBS[token]
    if not send(job, update, "", "run.delivery_ack") or not send(job, update, "accepted", "run.claim"):
        with LOCK:
            job["state"] = "claim_failed"
        return
    if not send(job, update, "running"):
        with LOCK:
            job["state"] = "running_failed"
        return
    with LOCK:
        job["state"] = "running"
    digest = hashlib.sha256(token.encode()).digest()
    rejected = False
    deadline = time.monotonic() + (1800 if "browser_" in token else 120)
    while time.monotonic() < deadline:
        with LOCK:
            text = "\n\n".join(job["texts"])
            mode = job["mode"]
        if job["stop"].is_set():
            if mode == "confirm" or job["confirm"].is_set():
                with LOCK:
                    job["state"] = "stopped"
                send(job, update, "stopped")
                return
            if mode == "race":
                break
            if (mode == "reject" or job["reject"].is_set()) and not rejected:
                rejected = True
                send(job, update, "stop_rejected")
        if job["finish"].is_set():
            break
        # Actual incremental CPU work advances independently of platform status.
        # Its counter continues for pending/rejected stops and freezes on confirm.
        for _ in range(256):
            digest = hashlib.sha256(digest + text.encode()).digest()
        with LOCK:
            job["ticks"] += 1
            job["digest"] = digest.hex()
        time.sleep(.02)
    else:
        with LOCK:
            job["state"] = "timed_out"
        return
    values = [int(value) for value in re.findall(r"\bVALUE=(-?\d+)\b", text)]
    result = json.dumps({"sum": sum(values), "ticks": job["ticks"], "digest": digest.hex()}, separators=(",", ":"))
    with LOCK:
        job["state"] = "completed"
    send(job, update, "completed", result=result)
