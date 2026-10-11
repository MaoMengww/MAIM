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
import run_control
import authorization
import compaction
import collaboration
import recovery

HOST = os.environ.get("HOST", "0.0.0.0")
PORT = int(os.environ.get("PORT", "8099"))
LOCK = threading.Lock()
SESSIONS = {}
EXECUTORS = {}
INTERACTION_GATES = {}
OBSERVATIONS = {"mcp_lists": 0, "tool_texts": [], "chat_models": [], "embed_models": [], "active_ingest": 0,
                "tool_call_ids": [], "chat_requests": [], "executor_requests": [], "executor_activities": []}
LLM_CHAT_OBSERVATIONS = []
COMPONENT_OBSERVATIONS = []
COMPONENT_PNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jfWQAAAAASUVORK5CYII="


def llm_component(body, handler, path):
    """Only implement third-party HTTP protocols; model identity and RPCs are AIM's."""
    model = body.get("model", "")
    if not model.startswith("e2e-component-"):
        return False
    vision = model.startswith("e2e-component-vlm-")
    rerank = model.startswith("e2e-component-rerank-")
    messages = body.get("messages", [])
    parts = messages[-1].get("content", []) if messages else []
    cohere = model.startswith("e2e-component-cohere-")
    inputs = body.get("texts" if cohere else "input", [])
    token = body.get("query", "") if rerank else (next((part.get("text", "") for part in parts if isinstance(part, dict)), "") if vision else (inputs[0] if inputs else ""))
    row = {"model": model, "token": token, "path": path, "body": body,
           "auth_digest": hashlib.sha256(handler.headers.get("Authorization", "").encode()).hexdigest(),
           "state": "received"}
    with LOCK:
        COMPONENT_OBSERVATIONS.append(row)

    def state(value):
        with LOCK:
            row["state"] = value

    if rerank:
        documents = body.get("documents", [])
        if (path != "/native/v2/rerank" or len(documents) != 2
                or body.get("top_n") != 1 or "return_documents" in body):
            state("invalid_request")
            handler._json(400, {"error": {"message": "native rerank protocol mismatch"}})
            return True
        if "HTTP_ERROR" in token:
            state("http_error")
            handler._json(503, {"error": {"message": "LLM_PROVIDER_PRIVATE_BODY|" + token + "|" + handler.headers.get("Authorization", "")}})
            return True
        index = next((i for i, document in enumerate(documents) if token in document), -1)
        if "INVALID_INDEX" in token:
            index = len(documents)
        response = {"results": [{"index": index, "relevance_score": .95}],
                    "meta": {"billed_units": {"search_units": 2}}}
        state("completed")
        handler._json(200, response)
        return True
    if vision:
        expected = [{"role": "system", "content": "component system"},
                    {"role": "user", "content": [{"type": "text", "text": token},
                                                  {"type": "image_url", "image_url": {"url": COMPONENT_PNG}}]}]
        valid = (path == "/v1/chat/completions" and messages == expected
                 and body.get("stream", False) is False and body.get("max_tokens") == 37
                 and body.get("temperature") == .25)
    elif cohere:
        valid = (path == "/native/v2/embed" and body.get("input_type") == "search_document"
                 and body.get("embedding_types") == ["float"]
                 and isinstance(inputs, list) and len(inputs) == 2 and "input" not in body)
    else:
        valid = path == "/v1/embeddings" and isinstance(inputs, list) and len(inputs) == 2
    if not valid:
        state("invalid_request")
        handler._json(400, {"error": {"message": "component protocol mismatch"}})
        return True
    if "HTTP_ERROR" in token:
        state("http_error")
        handler._json(400 if not vision else 503, {"error": {"message": "LLM_PROVIDER_PRIVATE_BODY|" + token + "|" + handler.headers.get("Authorization", "")}})
        return True
    if "REDIRECT" in token:
        state("redirected")
        handler.send_response(307)
        handler.send_header("Location", "/llm-components/redirect-target")
        handler.send_header("Content-Length", "0")
        handler.end_headers()
        return True
    if "CANCEL" in token:
        # Observe socket EOF from the gateway's real unary HTTP cancellation.
        import select
        import socket
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            try:
                readable, _, _ = select.select([handler.connection], [], [], .03)
                cancelled = readable and handler.connection.recv(1, socket.MSG_PEEK) == b""
            except (ConnectionResetError, ConnectionAbortedError):
                cancelled = True
            if cancelled:
                state("cancelled")
                handler.close_connection = True
                return True
        state("cancel_not_observed")
        handler._json(504, {"error": {"message": "cancellation not observed"}})
        return True
    if "OVERSIZED" in token:
        state("oversized")
        handler._json(200, {"padding": "x" * (17 * 1024 * 1024)})
        return True
    if vision:
        response = {"id": "component-vision", "object": "chat.completion", "created": 1, "model": model,
                    "choices": [{"index": 0, "message": {"role": "assistant", "content": "component-vision:" + token}, "finish_reason": "stop"}]}
        if "NO_USAGE" not in token:
            response["usage"] = ({"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0} if "ZERO" in token
                                 else {"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12})
    else:
        vectors = [[.1000000001, -.2000000001, 1.00000006], [.333333333333, 2.00000013, -.123456789]]
        if cohere:
            if "MISSING_VECTOR" in token:
                vectors.pop()
            elif "WRONG_DIMENSION" in token:
                vectors[0].pop()
            response = {"embeddings": {"float": vectors}}
            if "NO_USAGE" not in token:
                response["meta"] = {"billed_units": {"input_tokens": 0 if "ZERO" in token else 9}}
        else:
            data = [{"object": "embedding", "index": i, "embedding": values} for i, values in reversed(list(enumerate(vectors)))]
            if "EMPTY" in token:
                data = []
            elif "INVALID_INDEX" in token:
                data[0]["index"] = 2
            elif "NEGATIVE_INDEX" in token:
                data[0]["index"] = -1
            elif "DUPLICATE" in token:
                data[0]["index"] = data[1]["index"]
            elif "MISSING_INDEX" in token:
                del data[0]["index"]
            elif "MISSING_VECTOR" in token:
                data.pop()
            elif "WRONG_DIMENSION" in token:
                data[0]["embedding"].pop()
            response = {"object": "list", "model": model, "data": data}
            if "NO_USAGE" not in token:
                count = 0 if "ZERO" in token else 9
                response["usage"] = {"prompt_tokens": count, "total_tokens": count}
    state("completed")
    handler._json(200, response)
    return True


def llm_chat(body, handler, path):
    """Controlled HTTP provider only; AIM registry and RPC remain real."""
    model = body.get("model", "")
    if not model.startswith("e2e-llm-"):
        return False
    messages = body.get("messages", [])
    token = next((item.get("content", "") for item in reversed(messages)
                  if item.get("role") == "user"), "")
    row = {"model": model, "token": token, "path": path, "body": body,
           "auth_digest": hashlib.sha256(handler.headers.get("Authorization", "").encode()).hexdigest(),
           "state": "received", "usage_reported": False, "finish_sent": False, "done_sent": False}
    with LOCK:
        LLM_CHAT_OBSERVATIONS.append(row)
    def state(value):
        with LOCK:
            row["state"] = value
    if body.get("stream") is not True or body.get("temperature") != 0:
        state("invalid_request")
        handler._json(400, {"error": {"message": "stream=true and explicit zero temperature required"}})
        return True
    if token.startswith("LLM_REPLAY"):
        assistant = next((item for item in messages if item.get("tool_calls")), {})
        calls = assistant.get("tool_calls", [])
        results = [item for item in messages if item.get("role") == "tool"]
        expected = [("call-a", "fixture_echo_a", '{"text":"a"}'),
                    ("call-b", "fixture_echo_b", '{"text":"b"}')]
        actual = [(item.get("id"), item.get("function", {}).get("name"),
                   item.get("function", {}).get("arguments")) for item in calls]
        if (actual != expected or [(item.get("tool_call_id"), item.get("content")) for item in results]
                != [("call-a", "fixture-tool:a"), ("call-b", "fixture-tool:b")]
                or [item.get("role") for item in messages] != ["system", "user", "assistant", "tool", "tool"]
                or token.endswith("DEEPSEEK") and assistant.get("reasoning_content") != "private-tool-reasoning"):
            state("invalid_replay")
            handler._json(400, {"error": {"message": "tool order/result/reasoning mismatch"}})
            return True
    tools = body.get("tools", [])
    if token.startswith(("LLM_TOOLS", "LLM_REPLAY")):
        expected_schema = {"type": "object", "properties": {"text": {"type": "string", "minLength": 1}},
                           "required": ["text"], "additionalProperties": False,
                           "anyOf": [{"properties": {"text": {"enum": ["a", "b"]}}}]}
        if ([item.get("function", {}).get("name") for item in tools] != ["fixture_echo_a", "fixture_echo_b"]
                or any(item.get("function", {}).get("parameters") != expected_schema for item in tools)):
            state("invalid_schema")
            handler._json(400, {"error": {"message": "ordered tools and exact JSON Schema required"}})
            return True
    secret_error = "LLM_PROVIDER_PRIVATE_BODY|" + token + "|" + handler.headers.get("Authorization", "")
    if token == "LLM_HTTP_ERROR":
        state("http_error")
        handler._json(503, {"error": {"message": secret_error}})
        return True
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Connection", "close")
    handler.end_headers()
    handler.close_connection = True
    def event(delta=None, reason=None, usage=None, choices=True):
        chunk = {"id": "e2e-chat", "object": "chat.completion.chunk", "created": 1, "model": model,
                 "choices": [{"index": 0, "delta": delta or {}, "finish_reason": reason}] if choices else []}
        if reason is not None:
            with LOCK:
                row["finish_sent"] = True
        if usage is not None:
            chunk["usage"] = usage
            with LOCK:
                row["usage_reported"] = True
        handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
        handler.wfile.flush()
    try:
        event({"role": "assistant"})
        time.sleep(.18)  # A role-only delta must not start TTFT.
        if token in ("LLM_OVERSIZED_CONTENT", "LLM_OVERSIZED_TOOLS"):
            state("oversized")
            for index in range(272):
                if token == "LLM_OVERSIZED_CONTENT":
                    event({"content": "x" * 65536})
                else:
                    call = {"index": 0, "function": {"arguments": "x" * 65536}}
                    if index == 0:
                        call.update({"id": "large-call", "type": "function"})
                        call["function"]["name"] = "fixture_echo_a"
                    event({"tool_calls": [call]})
            event(reason="tool_calls" if token.endswith("TOOLS") else "stop")
            handler.wfile.write(b"data: [DONE]\n\n")
            handler.wfile.flush()
            return True
        if token.startswith("LLM_TOOLS"):
            # Deliberately arrive in reverse index order, and fragment arguments.
            event({"reasoning_content": "private-tool-reasoning"} if token.endswith("DEEPSEEK") else
                  {"tool_calls": [{"index": 1, "id": "call-b", "type": "function",
                                   "function": {"name": "fixture_echo_b", "arguments": '{"text":'}}]})
            if token.endswith("DEEPSEEK"):
                event({"tool_calls": [{"index": 1, "id": "call-b", "type": "function",
                                       "function": {"name": "fixture_echo_b", "arguments": '{"text":'}}]})
            event({"tool_calls": [{"index": 0, "id": "call-a", "type": "function",
                                   "function": {"name": "fixture_echo_a", "arguments": '{"text":'}}]})
            event({"tool_calls": [{"index": 1, "function": {"arguments": '"b"}'}},
                                   {"index": 0, "function": {"arguments": '"a"}'}}]})
            reason = "tool_calls"
        elif token == "LLM_REASONING":
            if not any(item.get("role") == "assistant" and item.get("reasoning_content") == "private-chain-of-thought" for item in messages):
                raise ValueError("missing private reasoning replay")
            event({"reasoning_content": "fixture-reasoning:"})
            event({"reasoning_content": "private-chain-of-thought"})
            event({"content": "fixture-chat:" + token})
            reason = "stop"
        else:
            event({"content": "fixture-chat:"})
            event({"content": token})
            reason = "stop"
        state("first_delta")
        if token == "LLM_CANCEL":
            # The socket write observes actual gateway HTTP cancellation, not just
            # a client-side gRPC cancellation status. No finish or DONE is sent.
            for _ in range(300):
                time.sleep(.03)
                handler.wfile.write(b": waiting-for-cancellation\n\n")
                handler.wfile.flush()
            state("cancel_not_observed")
            return True
        time.sleep(.3)  # TTFT must differ from full completion latency.
        if token == "LLM_STREAM_ERROR":
            handler.wfile.write(("data: " + json.dumps({"error": {"message": secret_error}}) + "\n\n").encode())
            handler.wfile.flush()
            state("stream_error")
            return True
        event(reason=None if token == "LLM_NO_FINISH" else reason)
        if token != "LLM_NO_USAGE":
            usage = {"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12}
            if token == "LLM_ZERO":
                usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
            elif token == "LLM_CACHE":
                usage["prompt_tokens_details"] = {"cached_tokens": 2, "cache_write_tokens": 3}
            elif token == "LLM_CACHE_ZERO":
                usage["prompt_tokens_details"] = {"cached_tokens": 0, "cache_write_tokens": 0}
            elif token == "LLM_DEEPSEEK_CACHE":
                usage.update({"prompt_cache_hit_tokens": 2, "prompt_cache_miss_tokens": 5})
            event(usage=usage, choices=False)
        if token != "LLM_NO_DONE":
            handler.wfile.write(b"data: [DONE]\n\n")
            handler.wfile.flush()
            with LOCK:
                row["done_sent"] = True
        state("truncated" if token in ("LLM_NO_DONE", "LLM_NO_FINISH") else "completed")
    except (BrokenPipeError, ConnectionResetError):
        state("cancelled")
    except ValueError:
        state("invalid_replay")
    return True
TOOL = {"name": "fixture_echo", "description": "Echo text over the MCP network", "inputSchema": {
    "type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]
}, "annotations": {"readOnlyHint": True, "destructiveHint": False}}


def vector(text):
    # A deterministic normalized 1536-dimensional external embedding protocol.
    # Known content and its query share a keyword direction; bulk documents do not.
    digest = hashlib.sha256(text.encode()).digest()
    values = [0.0] * 1536
    values[0 if "P5_KNOWN_ANCHOR" in text else 3 if "P5_FAIL_INGEST" in text else 1] = 1.0
    values[2] = digest[0] / 25500.0
    norm = math.sqrt(sum(value * value for value in values))
    return [value / norm for value in values]


def rpc_result(body, handler=None):
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
        if handler is not None and run_control.tool(body, handler):
            return {"jsonrpc": "2.0", "id": body.get("id"), "error": {"code": -32800, "message": "cancelled external computation"}}
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


def mixed_run_reply(body, messages):
    inputs = [value for item in messages if (value := accepted_input(item)) is not None]
    token = inputs[0]["content"]
    tools = body.get("tools") or []
    echo = next((item["function"]["name"] for item in tools
                 if "fixture_echo" in item.get("function", {}).get("name", "")), "")
    ask = next((item["function"]["name"] for item in tools
                if item.get("function", {}).get("name") == "ask_for_input"), "")
    if not echo or not ask:
        raise ValueError("mixed round requires network echo and input capabilities")
    ids = ["mixed-before", "mixed-ask", "mixed-after"]
    batches = [[call["id"] for call in item["tool_calls"]]
               for item in messages if item.get("tool_calls")]
    results = [item for item in messages if item.get("role") == "tool"]
    if not batches:
        if results or len(inputs) != 1:
            raise ValueError("mixed round lost its initial tool calls")
        calls = [
            {"id": ids[0], "type": "function", "function": {
                "name": echo, "arguments": json.dumps({"text": token + ":before"})}},
            {"id": ids[1], "type": "function", "function": {
                "name": ask, "arguments": json.dumps({"reason": "Supply the continuation text"})}},
            {"id": ids[2], "type": "function", "function": {
                "name": echo, "arguments": json.dumps({"text": token + ":after"})}},
        ]
        return {"role": "assistant", "content": None, "tool_calls": calls}, "tool_calls"
    if batches != [ids] or len(results) != len(ids) or {item.get("tool_call_id") for item in results} != set(ids):
        raise ValueError("mixed round requires each tool result paired exactly once")
    pending = set()
    for item in messages:
        if item.get("role") == "tool":
            call_id = item.get("tool_call_id")
            if call_id not in pending:
                raise ValueError("mixed round has an orphan or repeated tool response")
            pending.remove(call_id)
        else:
            if pending:
                raise ValueError("mixed round inserted input before completing tool responses")
            if item.get("tool_calls"):
                if item.get("role") != "assistant":
                    raise ValueError("mixed round tool calls require an assistant message")
                pending = {call["id"] for call in item["tool_calls"]}
    if pending:
        raise ValueError("mixed round has incomplete tool responses")
    by_id = {item["tool_call_id"]: item for item in results}
    for call_id, suffix in ((ids[0], ":before"), (ids[2], ":after")):
        if "fixture-tool:" + token + suffix not in str(by_id[call_id].get("content", "")):
            raise ValueError("mixed round lost its network tool result")
    if len(inputs) != 2 or not by_id[ids[1]].get("content"):
        raise ValueError("mixed round resumed without accepted input or input tool result")
    return {"role": "assistant", "content": "fixture-resumed:" + token + "|" + inputs[1]["content"]}, "stop"


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
    if body.get("bot_token"):
        raw = json.dumps(body["update"], separators=(",", ":")).encode()
        request = Request(config["gateway"] + "/api/v1/bot-app/runs/update", data=raw,
                          headers={"Content-Type": "application/json", "Authorization": "Bearer " + body["bot_token"]}, method="POST")
        try:
            response = urlopen(request, timeout=15)
        except HTTPError as error:
            response = error
        with response:
            return {"status": response.status, "body": json.loads(response.read(1024 * 1024))}
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
        if path.startswith("/recovery/") and path != "/recovery/mcp":
            self._json(200, recovery.command(path, body))
            return
        if path.startswith("/compaction/"):
            self._json(200, compaction.command(path, body))
            return
        if path.startswith("/authorization/") and path != "/authorization/mcp":
            self._json(200, authorization.command(path, body))
            return
        if path == "/llm-components/observe":
            with LOCK:
                snapshot = json.loads(json.dumps([row for row in COMPONENT_OBSERVATIONS if row["model"] == body.get("model")]))
            self._json(200, {"requests": snapshot})
            return
        if path == "/llm-components/redirect-target":
            with LOCK:
                COMPONENT_OBSERVATIONS.append({"model": body.get("model", ""), "token": "redirect-target", "state": "redirect_followed", "path": path})
            self._json(400, {"error": {"message": "redirect must not be followed"}})
            return
        if path in ("/v1/chat/completions", "/v1/embeddings", "/native/v2/embed", "/native/v2/rerank") and llm_component(body, self, path):
            return
        if path == "/llm-chat/observe":
            with LOCK:
                rows = [row for row in LLM_CHAT_OBSERVATIONS if row["model"] == body.get("model")]
                snapshot = json.loads(json.dumps(rows))
            self._json(200, {"requests": snapshot})
            return
        if path in ("/v1/chat/completions", "/v1-changed/chat/completions") and llm_chat(body, self, path):
            return
        if path in ("/run-control/arm", "/run-control/observe", "/run-control/release", "/run-control/finish", "/run-control/confirm", "/run-control/reject"):
            self._json(200, run_control.command(path, body))
            return
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
            if valid and body.get("type") == "bot.run.request":
                run_control.accept(body, executor_update)
            self._json(200 if valid else 403, {"delivered": valid})
            return
        if path == "/v1/chat/completions":
            messages = body.get("messages") or []
            text = user_text(messages)
            model = body.get("model", "")
            with LOCK:
                OBSERVATIONS["chat_models"].append(model)
                OBSERVATIONS["chat_requests"].append(body)
            if compaction.model(body, self, accepted_input):
                return
            if run_control.model(body, self, text):
                return
            if authorization.model(body, self, accepted_input, user_text):
                return
            if recovery.model(body, self, accepted_input, user_text):
                return
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
            if model.startswith("fixture-collaboration-"):
                try:
                    message, reason = collaboration.reply(body, accepted_input)
                except (KeyError, ValueError, TypeError) as error:
                    self._json(400, {"error": {"message": str(error)}})
                    return
            elif model.startswith("fixture-interaction-"):
                try:
                    message, reason = interaction_reply(body, messages)
                except (KeyError, ValueError, TypeError) as error:
                    self._json(400, {"error": {"message": str(error)}})
                    return
            elif any((accepted_input(item) or {}).get("content", "").startswith("RUN_MIXED_WAIT_") for item in messages):
                try:
                    message, reason = mixed_run_reply(body, messages)
                except (KeyError, ValueError, TypeError) as error:
                    self._json(400, {"error": {"message": str(error)}})
                    return
            elif any((accepted_input(item) or {}).get("content", "").startswith("RUN_BUDGET_WAIT_") for item in messages):
                inputs = [value for item in messages if (value := accepted_input(item)) is not None]
                message = {"role": "assistant", "content": None, "tool_calls": [{
                    "id": "budget-ask-" + str(len(inputs)), "type": "function", "function": {
                        "name": "ask_for_input", "arguments": json.dumps({"reason": "Supply the next budget input"})}}]}
                reason = "tool_calls"
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
                    deltas = [{"role": "assistant", "tool_calls": [dict(call, index=index)
                               for index, call in enumerate(message["tool_calls"])]}]
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
        elif path == "/recovery/mcp":
            result = recovery.rpc(body) if body.get("method") in ("tools/list", "tools/call") else rpc_result(body, self)
            if result is None:
                self._json(202, {})
            else:
                self._sse(result)
        elif path == "/authorization/mcp":
            result = authorization.rpc(body) if body.get("method") in ("tools/list", "tools/call") else rpc_result(body, self)
            if result is None:
                self._json(202, {})
            else:
                self._sse(result)
        elif path == "/mcp/sse":
            result = rpc_result(body, self)
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
            result = rpc_result(body, self)
            if result is not None:
                events.put(result)
            self._json(202, {})
        else:
            self._json(404, {"error": "not found"})

    def log_message(self, format, *args):
        return


if __name__ == "__main__":
    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()
