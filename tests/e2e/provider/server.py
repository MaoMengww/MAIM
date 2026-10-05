from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import json
import math
import os
import queue
import threading
import time
import uuid
from urllib.parse import urlparse, parse_qs

HOST = os.environ.get("HOST", "0.0.0.0")
PORT = int(os.environ.get("PORT", "8099"))
LOCK = threading.Lock()
SESSIONS = {}
OBSERVATIONS = {"mcp_lists": 0, "tool_texts": [], "chat_models": [], "embed_models": [], "active_ingest": 0}
TOOL = {"name": "fixture_echo", "description": "Echo text over the MCP network", "inputSchema": {
    "type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]
}}


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
        try:
            body = json.loads(self.rfile.read(length))
        except json.JSONDecodeError:
            self._json(400, {"error": "invalid json"})
            return
        path = urlparse(self.path).path
        if path == "/v1/chat/completions":
            messages = body.get("messages") or []
            text = next((str(item.get("content", "")) for item in reversed(messages) if item.get("role") == "user"), "")
            model = body.get("model", "")
            with LOCK:
                OBSERVATIONS["chat_models"].append(model)
            tool_messages = [item for item in messages if item.get("role") == "tool"]
            if not tool_messages:
                tools = body.get("tools") or []
                echo = next((item["function"]["name"] for item in tools if "fixture_echo" in item.get("function", {}).get("name", "")), "")
                if not echo:
                    self._json(400, {"error": {"message": "MCP fixture_echo tool required; fallback is not accepted", "type": "invalid_request_error"}})
                    return
                message = {"role": "assistant", "content": None, "tool_calls": [{"id": "fixture-call", "type": "function", "function": {
                    "name": echo, "arguments": json.dumps({"text": text})}}]}
                reason = "tool_calls"
            else:
                if not any("fixture-tool:" + text in str(item.get("content", "")) for item in tool_messages):
                    self._json(400, {"error": {"message": "network tool result mismatch"}})
                    return
                message = {"role": "assistant", "content": "fixture-reply:" + text}
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
        elif path == "/v1/embeddings":
            inputs = body.get("input", [])
            if isinstance(inputs, str):
                inputs = [inputs]
            if any("P5_FAIL_INGEST" in value for value in inputs):
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
