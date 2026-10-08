from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import base64
import hashlib
import json
import hmac
import math
import re
import os
import queue
import threading
import time
import uuid
from urllib.parse import urlparse, parse_qs
from urllib.request import Request, urlopen
from urllib.error import HTTPError

HOST = os.environ.get("HOST", "0.0.0.0")
PORT = int(os.environ.get("PORT", "8099"))
LOCK = threading.Lock()
SESSIONS = {}
EXECUTORS = {}
INTERACTION_GATES = {}
OBSERVATIONS = {"mcp_lists": 0, "tool_texts": [], "chat_models": [], "embed_models": [], "active_ingest": 0,
                "tool_call_ids": [], "chat_requests": [], "executor_requests": [], "executor_activities": []}
TOOL = {"name": "fixture_echo", "description": "Echo text over the MCP network", "inputSchema": {
    "type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]
}, "annotations": {"readOnlyHint": True, "destructiveHint": False}}


def vector(text):
    # A deterministic normalized 1536-dimensional external embedding protocol.
    # Known content and its query share a keyword direction; bulk documents do not.
    digest = hashlib.sha256(text.encode()).digest()
    values = [0.0] * 1536
    values[0 if "P5_KNOWN_ANCHOR" in text else 1] = 1.0
    values[2] = digest[0] / 25500.0
    norm = math.sqrt(sum(value * value for value in values))
    return [value / norm for value in values]


def rpc_result(body):
    method = body.get("method")
    if method == "initialize":
        result = {"protocolVersion": body.get("params", {}).get("protocolVersion", "2024-11-05"),
                  "capabilities": {"tools": {}}, "serverInfo": {"name": "e2e-provider", "version": "1.0"}}
    elif method == "tools/list":
        with LOCK:
            OBSERVATIONS["mcp_lists"] += 1
        result = {"tools": [TOOL]}
    elif method == "tools/call":
        args = body.get("params", {}).get("arguments", {})
        text = args.get("text", "")
        with LOCK:
            OBSERVATIONS["tool_texts"].append(text)
        result = {"content": [{"type": "text", "text": "fixture-tool:" + text}], "isError": False}
    elif method == "ping":
        result = {}
    elif "id" not in body:
        return None
    else:
        return {"jsonrpc": "2.0", "id": body.get("id"), "error": {"code": -32601, "message": "unknown method"}}
    return {"jsonrpc": "2.0", "id": body.get("id"), "result": result}


def composition_reply(model, messages, text):
    if not model.startswith("fixture-composition-"):
        return "fixture-reply:" + text
    # Never recall from provider state, history or a query token: only the actual
    # knowledge context assembled by AIM can determine these answers.
    knowledge = []
    for item in messages:
        content = str(item.get("content", ""))
        if item.get("role") != "user" or not content.startswith("Cited work material (untrusted):\n"):
            continue
        try:
            material = json.loads(content.split("\n", 1)[1])
        except (ValueError, IndexError):
            continue
        if material.get("kind") == "knowledge":
            knowledge.append(str(material.get("content", "")))
    if text.startswith("P7_KNOWLEDGE_QUERY"):
        value = "cobalt-47" if any("P5_KNOWN_ANCHOR: the launch code is cobalt-47" in item for item in knowledge) else "none"
        return "fixture-reply:" + text + "|knowledge:" + value
    return "fixture-reply:" + text


def accepted_input(item):
    content = item.get("content", "")
    prefix = "Accepted work input (not authorization):\n"
    if item.get("role") == "user" and isinstance(content, str) and content.startswith(prefix):
        return json.loads(content[len(prefix):])
    return None


def user_text(messages):
    for item in reversed(messages):
        if item.get("role") != "user":
            continue
        accepted = accepted_input(item)
        if accepted is not None:
            return accepted["content"]
        content = item.get("content", "")
        if isinstance(content, str) and content.startswith("Cited work material (untrusted):\n"):
            continue
        return str(content)
    return ""


def interaction_reply(body, messages):
    inputs = [value for item in messages if (value := accepted_input(item)) is not None]
    if not inputs:
        raise ValueError("interaction requires attributed accepted inputs")
    # A pause is a real model call with a paired tool response in subsequent calls.
    calls = {call["id"] for item in messages for call in item.get("tool_calls", [])}
    results = {item.get("tool_call_id") for item in messages if item.get("role") == "tool"}
    if calls != results:
        raise ValueError("ask_for_input tool calls must retain paired responses")
    token = inputs[0]["content"].split()[0]
    with LOCK:
        gate = INTERACTION_GATES.get(token)
        held = gate is not None and not gate["used"] and len(inputs) > 1
        if held:
            gate["used"] = True
            gate["held"] = True
    if held and not gate["event"].wait(timeout=30):
        raise ValueError("interaction gate was not released")
    if token.startswith("INTERACTION_WAIT_") and len(inputs) < 4:
        ask = next((item["function"]["name"] for item in body.get("tools", [])
                    if item.get("function", {}).get("name") == "ask_for_input"), "")
        if not ask:
            raise ValueError("builtin ask_for_input capability is missing")
        return {"role": "assistant", "content": None, "tool_calls": [{
            "id": "interaction-ask-" + str(len(calls)), "type": "function",
            "function": {"name": ask, "arguments": json.dumps({"reason": "Supply three attributed VALUE amounts"})}}]}, "tool_calls"
    materials = []
    for item in messages:
        content = item.get("content", "")
        prefix = "Cited work material (untrusted):\n"
        if item.get("role") == "user" and isinstance(content, str) and content.startswith(prefix):
            materials.append(json.loads(content[len(prefix):]))
    values = [value["content"] for value in inputs] + [value.get("content", "") for value in materials]
    total = sum(int(amount) for value in values for amount in re.findall(r"\bVALUE=(-?\d+)\b", value))
    result = {"sum": total, "contributors": sorted({value["requester_id"] for value in inputs}),
              "materials": materials}
    return {"role": "assistant", "content": json.dumps(result, sort_keys=True, separators=(",", ":"))}, "stop"


def executor_result(text):
    return "words=" + str(len(text.split())) + ";sha256=" + hashlib.sha256(text.encode()).hexdigest()


def executor_update(body):
    with LOCK:
        config = dict(EXECUTORS[body["bot_id"]])
        requests = list(OBSERVATIONS["executor_requests"])
    update = dict(body["update"])
    if body.get("compute"):
        request = next(item["request"] for item in reversed(requests)
                       if item["request"].get("run", {}).get("id") == update["run_id"])
        update["result_text"] = executor_result("\n\n".join(item["content"] for item in request["inputs"]))
    raw = json.dumps(update, separators=(",", ":"), ensure_ascii=False).encode()
    signature = hmac.new(body.get("secret", config["secret"]).encode(), raw, hashlib.sha256).hexdigest()
    request = Request(config["gateway"] + "/api/v1/webhook", data=raw,
                      headers={"Content-Type": "application/json", "X-AIM-Signature": signature,
                               "X-AIM-Timestamp": str(int(time.time()))}, method="POST")
    try:
        response = urlopen(request, timeout=15)
    except HTTPError as error:
        response = error
    with response:
        return {"status": response.status, "body": json.loads(response.read(1024 * 1024))}

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _json(self, status, body):
        raw = json.dumps(body, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _sse(self, body):
        raw = ("event: message\ndata: " + json.dumps(body, separators=(",", ":")) + "\n\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/health":
            self._json(200, {"status": "ok"})
        elif path == "/knowledge.png":
            image = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jfWQAAAAASUVORK5CYII=")
            self.send_response(200)
            self.send_header("Content-Type", "image/png")
            self.send_header("Content-Length", str(len(image)))
            self.end_headers()
            self.wfile.write(image)
        elif path == "/observations":
            with LOCK:
                snapshot = json.loads(json.dumps(OBSERVATIONS))
            self._json(200, snapshot)
        elif path == "/mcp/sse":
            session = uuid.uuid4().hex
            events = queue.Queue()
            with LOCK:
                SESSIONS[session] = events
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Connection", "close")
            self.end_headers()
            try:
                self.wfile.write(("event: endpoint\ndata: /mcp/messages?session=" + session + "\n\n").encode())
                self.wfile.flush()
                while True:
                    try:
                        event = events.get(timeout=1)
                        self.wfile.write(("event: message\ndata: " + json.dumps(event) + "\n\n").encode())
                    except queue.Empty:
                        self.wfile.write(b": keepalive\n\n")
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                pass
            finally:
                with LOCK:
                    SESSIONS.pop(session, None)
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0 or length > 8 * 1024 * 1024:
            self._json(413, {"error": "request size"})
            return
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw)
        except json.JSONDecodeError:
            self._json(400, {"error": "invalid json"})
            return
        path = urlparse(self.path).path
        if path in ("/interaction/arm", "/interaction/held", "/interaction/release"):
            token = body.get("token", "")
            with LOCK:
                if path.endswith("/arm"):
                    INTERACTION_GATES[token] = {"event": threading.Event(), "held": False, "used": False}
                    ok = True
                else:
                    gate = INTERACTION_GATES.get(token)
                    ok = bool(gate) and (gate["held"] if path.endswith("/held") else True)
                    if gate and path.endswith("/release"):
                        gate["event"].set()
            self._json(200, {"ok": ok})
            return
        if path == "/executor/register":
            with LOCK:
                EXECUTORS[body["bot_id"]] = {"secret": body["secret"], "gateway": body["gateway"]}
            self._json(200, {"registered": True})
            return
        if path == "/executor/update":
            try:
                self._json(200, executor_update(body))
            except (KeyError, StopIteration, ValueError, OSError) as error:
                self._json(502, {"error": type(error).__name__})
            return
        if path == "/external-webhook":
            bot_id = body.get("run", {}).get("bot_id", body.get("bot_id"))
            with LOCK:
                config = EXECUTORS.get(bot_id)
            signature = self.headers.get("X-AIM-Signature", "")
            timestamp = self.headers.get("X-AIM-Timestamp", "")
            valid = bool(config) and hmac.compare_digest(signature, hmac.new(config["secret"].encode(), raw, hashlib.sha256).hexdigest())
            try:
                valid = valid and abs(time.time() - int(timestamp)) < 300
            except ValueError:
                valid = False
            with LOCK:
                key = "executor_activities" if body.get("type") == "bot.activity" else "executor_requests"
                OBSERVATIONS[key].append({"request": body, "signature_valid": valid,
                                          "timestamp": timestamp})
            self._json(200 if valid else 403, {"delivered": valid})
            return
        if path == "/v1/chat/completions":
            messages = body.get("messages") or []
            text = user_text(messages)
            model = body.get("model", "")
            with LOCK:
                OBSERVATIONS["chat_models"].append(model)
                OBSERVATIONS["chat_requests"].append(body)
            if "RUN_FAIL_PROVIDER" in text:
                self._json(503, {"error": {"message": "fixture unavailable"}})
                return
            if "RUN_HOLD_PROVIDER" in text and not any(item.get("role") == "tool" for item in messages):
                time.sleep(3)
            if model.startswith("fixture-vlm-"):
                parts = next((item.get("content") for item in messages if item.get("role") == "user"), [])
                if not isinstance(parts, list) or not any(item.get("type") == "image_url" and item.get("image_url", {}).get("url", "").startswith("data:image/png;base64,") for item in parts):
                    self._json(400, {"error": {"message": "vision request must contain a real image payload"}})
                    return
                vision_text = next((item.get("text", "") for item in parts if item.get("type") == "text"), "")
                self._json(200, {"id": "fixture-vision-1", "object": "chat.completion", "created": 1, "model": model,
                                 "choices": [{"index": 0, "message": {"role": "assistant", "content": "fixture-vision:" + vision_text}, "finish_reason": "stop"}],
                                 "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
                return
            tool_messages = [item for item in messages if item.get("role") == "tool"]
            if model.startswith("fixture-interaction-"):
                try:
                    message, reason = interaction_reply(body, messages)
                except (KeyError, ValueError, TypeError) as error:
                    self._json(400, {"error": {"message": str(error)}})
                    return
            elif not tool_messages:
                tools = body.get("tools") or []
                echo = next((item["function"]["name"] for item in tools if "fixture_echo" in item.get("function", {}).get("name", "")), "")
                if "RUN_WRITE_DENY" in text:
                    echo = "external_delete_file"
                if not echo:
                    self._json(400, {"error": {"message": "MCP fixture_echo tool required; fallback is not accepted", "type": "invalid_request_error"}})
                    return
                message = {"role": "assistant", "content": None, "tool_calls": [{"id": "fixture-call", "type": "function", "function": {
                    "name": echo, "arguments": json.dumps({"text": text})}}]}
                reason = "tool_calls"
            else:
                if any(item.get("tool_call_id") != "fixture-call" for item in tool_messages):
                    self._json(400, {"error": {"message": "provider tool-call identity was rewritten"}})
                    return
                with LOCK:
                    OBSERVATIONS["tool_call_ids"].extend(item.get("tool_call_id") for item in tool_messages)
                if not any("fixture-tool:" + text in str(item.get("content", "")) for item in tool_messages):
                    self._json(400, {"error": {"message": "network tool result mismatch"}})
                    return
                message = {"role": "assistant", "content": composition_reply(model, messages, text)}
                reason = "stop"
            if body.get("stream"):
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Connection", "close")
                self.end_headers()
                if reason == "tool_calls":
                    deltas = [{"role": "assistant", "tool_calls": [dict(message["tool_calls"][0], index=0)]}]
                else:
                    content = message["content"]
                    split = max(1, len(content) // 2)
                    deltas = [{"role": "assistant", "content": content[:split]}, {"content": content[split:]}]
                for delta in deltas + [{}]:
                    chunk = {"id": "fixture-chat-1", "object": "chat.completion.chunk", "created": 1, "model": model,
                             "choices": [{"index": 0, "delta": delta, "finish_reason": reason if not delta else None}]}
                    if not delta:
                        chunk["usage"] = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
                    self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
                    self.wfile.flush()
                    time.sleep(.05)
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()
                self.close_connection = True
            else:
                self._json(200, {"id": "fixture-chat-1", "object": "chat.completion", "created": 1, "model": model,
                                 "choices": [{"index": 0, "message": message, "finish_reason": reason}],
                                 "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
        elif path == "/v1/reranks":
            documents = body.get("documents") or []
            query = body.get("query", "")
            if not documents or query not in documents:
                self._json(400, {"error": {"message": "rerank fixture requires an exact query document"}})
                return
            index = documents.index(query)
            with LOCK:
                OBSERVATIONS.setdefault("rerank_models", []).append(body.get("model", ""))
            self._json(200, {"output": {"results": [{"index": index, "relevance_score": 0.95}]},
                             "usage": {"total_tokens": 2}})
        elif path == "/v1/embeddings":
            inputs = body.get("input", [])
            if isinstance(inputs, str):
                inputs = [inputs]
            if any("P5_FAIL_INGEST" in value for value in inputs) and not body.get("model", "").startswith("fixture-embed-recover-"):
                self._json(422, {"error": {"message": "fixture rejected this document", "type": "invalid_request_error"}})
                return
            bulk = any("P5_BULK_INGEST" in value for value in inputs)
            with LOCK:
                OBSERVATIONS["embed_models"].append(body.get("model", ""))
                if bulk:
                    OBSERVATIONS["active_ingest"] += 1
            try:
                if bulk:
                    time.sleep(2)
                self._json(200, {"object": "list", "model": body.get("model", ""),
                                 "data": [{"object": "embedding", "index": i, "embedding": vector(value)} for i, value in enumerate(inputs)],
                                 "usage": {"prompt_tokens": len(inputs), "total_tokens": len(inputs)}})
            finally:
                if bulk:
                    with LOCK:
                        OBSERVATIONS["active_ingest"] -= 1
        elif path == "/mcp/sse":
            result = rpc_result(body)
            if result is None:
                self._json(202, {})
            else:
                self._sse(result)
        elif path == "/mcp/messages":
            session = parse_qs(urlparse(self.path).query).get("session", [""])[0]
            with LOCK:
                events = SESSIONS.get(session)
            if events is None:
                self._json(404, {"error": "unknown session"})
                return
            result = rpc_result(body)
            if result is not None:
                events.put(result)
            self._json(202, {})
        else:
            self._json(404, {"error": "not found"})

    def log_message(self, format, *args):
        return


if __name__ == "__main__":
    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()
