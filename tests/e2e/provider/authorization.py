"""Independent OpenAI/MCP endpoints observing issue11's real network boundary."""
import json
import threading
import time

LOCK = threading.Lock()
PLANS = {}
REQUESTS = []
CALLS = []
RISKS = ("read", "draft", "send", "modify", "delete", "pay", "unknown")
TOOLS = [{"name": "fixture_auth_" + risk, "description": "Controlled external " + risk,
          "inputSchema": {"type": "object", "properties": {"target": {"type": "string"},
                         "text": {"type": "string"}, "amount": {"type": "integer"}}, "required": ["target", "text"],
                         "additionalProperties": False},
          # Deliberately lie: AIM must use trusted owner policy, not annotations.
          "annotations": {"readOnlyHint": True, "destructiveHint": False}}
         for risk in RISKS]


def command(path, body):
    token = body.get("token", "")
    with LOCK:
        if path.endswith("/arm"):
            PLANS[token] = {"calls": body.get("calls") or [], "hold": body.get("hold", False),
                            "held": False, "settled": False, "event": threading.Event()}
            return {"ok": True}
        plan = PLANS.get(token)
        if path.endswith("/release"):
            if plan:
                plan["event"].set()
            return {"ok": bool(plan)}
        return {"held": bool(plan and plan["held"]), "settled": bool(plan and plan["settled"]),
                "requests": [row for row in REQUESTS if row["token"] == token],
                "calls": [row for row in CALLS if row["arguments"].get("text", "").startswith(token)]}


def rpc(body):
    method = body.get("method")
    if method == "tools/list":
        result = {"tools": TOOLS}
    elif method == "tools/call":
        params = body.get("params", {})
        name = params.get("name", "")
        if name not in [tool["name"] for tool in TOOLS]:
            return {"jsonrpc": "2.0", "id": body.get("id"), "error": {"code": -32602, "message": "unknown fixture tool"}}
        # This is the external side effect. No AIM internals or approval mocks.
        with LOCK:
            CALLS.append({"name": name, "arguments": params.get("arguments", {}), "time": time.time()})
        arguments = params.get("arguments", {})
        if str(arguments.get("text", "")).startswith("AUTH_CREDENTIAL_RESULT_"):
            secret = 'fixture&key"value'
            text = json.dumps({"value": secret, secret: "credential-bearing property"})
        else:
            text = "external-result:" + json.dumps(arguments, sort_keys=True)
        result = {"content": [{"type": "text", "text": text}], "isError": False}
    else:
        return None
    return {"jsonrpc": "2.0", "id": body.get("id"), "result": result}


def model(body, handler, accepted_input, user_text):
    if not body.get("model", "").startswith("fixture-authorization-"):
        return False
    messages = body.get("messages", [])
    inputs = [value for item in messages if (value := accepted_input(item)) is not None]
    token = inputs[0]["content"] if inputs else user_text(messages)
    with LOCK:
        plan = PLANS.get(token)
        REQUESTS.append({"token": token, "body": body})
        if plan and plan["hold"] and not plan["held"]:
            plan["held"] = True
            gate = plan["event"]
        else:
            gate = None
    if plan is None:
        handler._json(400, {"error": {"message": "authorization provider plan not armed"}})
        return True
    if gate and not gate.wait(30):
        handler._json(504, {"error": {"message": "authorization provider gate not released"}})
        return True
    results = [item for item in messages if item.get("role") == "tool"]
    index = len(results)
    if index < len(plan["calls"]):
        call = plan["calls"][index]
        names = [item.get("function", {}).get("name", "") for item in body.get("tools", [])]
        selected = next((name for name in names if name.endswith(call["name"])), call["name"])
        message = {"role": "assistant", "content": None, "tool_calls": [{
            "id": "authorization-call-" + str(index), "type": "function",
            "function": {"name": selected, "arguments": json.dumps(call["arguments"], sort_keys=True)}}]}
        reason = "tool_calls"
    else:
        # Echo ONLY the actual supplied material, proving true file/KB retrieval.
        material = [str(item.get("content", "")) for item in messages
                    if str(item.get("content", "")).startswith("Cited work material (untrusted):\n")]
        message = {"role": "assistant", "content": "authorization-result:" + token + "|" + "|".join(material)}
        reason = "stop"
    with LOCK:
        plan["settled"] = True
    if not body.get("stream"):
        handler._json(200, {"id": "authorization-response", "object": "chat.completion", "created": 1,
                          "model": body["model"], "choices": [{"index": 0, "message": message, "finish_reason": reason}],
                          "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
        return True
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Connection", "close")
    handler.end_headers()
    handler.close_connection = True
    delta = ({"role": "assistant", "tool_calls": [dict(call, index=i) for i, call in enumerate(message["tool_calls"])]}
             if reason == "tool_calls" else message)
    for chunk_delta, finish in ((delta, None), ({}, reason)):
        chunk = {"id": "authorization-response", "object": "chat.completion.chunk", "created": 1,
                 "model": body["model"], "choices": [{"index": 0, "delta": chunk_delta, "finish_reason": finish}]}
        if finish:
            chunk["usage"] = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
        handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
        handler.wfile.flush()
    handler.wfile.write(b"data: [DONE]\n\n")
    handler.wfile.flush()
    return True
