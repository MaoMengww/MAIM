"""External recovery ledger and gates; no AIM state or RPC is simulated."""
import json
import threading

import authorization

LOCK = threading.Lock()
PLANS = {}


def snapshot(plan):
    return {"model_requests": plan["model_requests"], "tool_calls": plan["tool_calls"],
            "held": plan["held"], "inputs": list(plan["inputs"]),
            "responses_finished": plan["responses_finished"]}


def command(path, body):
    token = body.get("token", "")
    with LOCK:
        if path.endswith("/arm"):
            PLANS[token] = {"mode": body["mode"], "model_requests": 0, "tool_calls": 0,
                            "held": "", "inputs": [], "used": False, "responses_finished": False,
                            "event": threading.Event()}
        plan = PLANS.get(token)
        if plan is None:
            raise ValueError("recovery plan not armed")
        if path.endswith("/release"):
            plan["event"].set()
        return snapshot(plan)


def hold(plan, phase):
    with LOCK:
        if plan["used"]:
            return
        plan["used"] = True
        plan["held"] = phase
    if not plan["event"].wait(240):
        raise TimeoutError("recovery gate was not released")


def rpc(body):
    method = body.get("method")
    if method == "tools/list":
        return {"jsonrpc": "2.0", "id": body.get("id"), "result": {"tools": authorization.TOOLS}}
    if method != "tools/call":
        return None
    params = body.get("params", {})
    arguments = params.get("arguments", {})
    token = arguments.get("text", "")
    with LOCK:
        plan = PLANS.get(token)
        if plan is None:
            raise ValueError("recovery tool is outside an armed ledger")
        # This count is the actual external operation, before any network reply.
        plan["tool_calls"] += 1
        mode = plan["mode"]
    if mode in ("unknown-write", "uncertain-read", "uncertain-draft"):
        hold(plan, "tool-after-effect")
    return {"jsonrpc": "2.0", "id": body.get("id"), "result": {
        "content": [{"type": "text", "text": "external-recovery-result:" + json.dumps(arguments, sort_keys=True)}],
        "isError": False}}


def model(body, handler, accepted_input, user_text):
    if not body.get("model", "").startswith("fixture-recovery-"):
        return False
    messages = body.get("messages", [])
    inputs = [item for message in messages if (item := accepted_input(message)) is not None]
    token = inputs[0]["content"] if inputs else user_text(messages)
    with LOCK:
        plan = PLANS.get(token)
        if plan:
            plan["model_requests"] += 1
            plan["inputs"] = [item["content"] for item in inputs]
    if plan is None:
        handler._json(400, {"error": {"message": "recovery model plan not armed"}})
        return True
    mode = plan["mode"]
    results = [message for message in messages if message.get("role") == "tool"]
    if mode == "waiting-input" and len(inputs) > 1:
        message = {"role": "assistant", "content": "recovery-result:" + token + "|" + inputs[-1]["content"]}
        reason = "stop"
    elif not results:
        risk = "send" if mode in ("write", "unknown-write", "completed-tool") else "draft" if mode == "uncertain-draft" else "read"
        advertised = [item.get("function", {}).get("name", "") for item in body.get("tools", [])]
        name = next((name for name in advertised if name.endswith("fixture_auth_" + risk)), "")
        if not name:
            handler._json(400, {"error": {"message": "real recovery MCP tool not advertised"}})
            return True
        calls = [{"id": "recovery-external-call", "type": "function", "function": {
            "name": name, "arguments": json.dumps({"target": "controlled-recovery-ledger", "text": token})}}]
        if mode == "waiting-input":
            calls.append({"id": "recovery-input-call", "type": "function", "function": {
                "name": "ask_for_input", "arguments": json.dumps({"reason": "Supply recovery input"})}})
        message = {"role": "assistant", "content": None, "tool_calls": calls}
        reason = "tool_calls"
    else:
        if mode in ("completed-tool", "publication"):
            hold(plan, "model-after-tool")
        message = {"role": "assistant", "content": "recovery-result:" + token}
        reason = "stop"
    if not body.get("stream"):
        handler._json(200, {"id": "recovery-response", "object": "chat.completion", "created": 1,
                           "model": body["model"], "choices": [{"index": 0, "message": message, "finish_reason": reason}],
                           "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
        if reason == "stop":
            with LOCK:
                plan["responses_finished"] = True
        return True
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Connection", "close")
    handler.end_headers()
    handler.close_connection = True
    delta = ({"role": "assistant", "tool_calls": [dict(call, index=index) for index, call in enumerate(message["tool_calls"])]}
             if reason == "tool_calls" else message)
    for content, finish in ((delta, None), ({}, reason)):
        chunk = {"id": "recovery-response", "object": "chat.completion.chunk", "created": 1,
                 "model": body["model"], "choices": [{"index": 0, "delta": content, "finish_reason": finish}]}
        if finish:
            chunk["usage"] = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
        handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
        handler.wfile.flush()
    handler.wfile.write(b"data: [DONE]\n\n")
    handler.wfile.flush()
    if reason == "stop":
        with LOCK:
            plan["responses_finished"] = True
    return True
