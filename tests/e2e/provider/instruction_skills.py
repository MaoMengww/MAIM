"""Controlled provider for instruction-skill evaluation and progressive loading.

The fixture verifies the actual model request: the frozen catalog in the system
prompt, the declared runtime tools, and the persisted skill content. It never
claims skill correctness; the client asserts a concrete expected result.
"""
import json
import threading
import time

LOCK = threading.Lock()
SESSIONS = {}


def _response(message, reason):
    return {"id": "fixture-skill-1", "object": "chat.completion", "created": 1,
            "model": "fixture-skill", "choices": [{"index": 0, "message": message, "finish_reason": reason}],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}}


def _emit(handler, message, reason, stream):
    if not stream:
        handler._json(200, _response(message, reason))
        return
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Connection", "close")
    handler.end_headers()
    if reason == "tool_calls":
        deltas = [{"role": "assistant", "tool_calls": [dict(call, index=index)
                   for index, call in enumerate(message["tool_calls"])]}]
    else:
        content = message["content"]
        split = max(1, len(content) // 2)
        deltas = [{"role": "assistant", "content": content[:split]}, {"content": content[split:]}]
    for delta in deltas + [{}]:
        chunk = {"id": "fixture-skill-1", "object": "chat.completion.chunk", "created": 1,
                 "model": "fixture-skill",
                 "choices": [{"index": 0, "delta": delta, "finish_reason": reason if not delta else None}]}
        if not delta:
            chunk["usage"] = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
        handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
        handler.wfile.flush()
        time.sleep(.02)
    handler.wfile.write(b"data: [DONE]\n\n")
    handler.wfile.flush()
    handler.close_connection = True


def _tool_call(call_id, name, arguments):
    return {"role": "assistant", "content": None, "tool_calls": [{
        "id": call_id, "type": "function",
        "function": {"name": name, "arguments": json.dumps(arguments)}}]}


def _text(messages):
    return "\n".join(str(item.get("content", "")) for item in messages)


def command(path, body):
    token = body.get("token", "")
    with LOCK:
        if path.endswith("/arm"):
            session = SESSIONS.setdefault(token, {"requests": []})
            session.update({
                "version_id": body["version_id"], "evaluation": body["evaluation"],
                "answer": body["answer"], "body_marker": body["body_marker"],
                "resource": body.get("resource", ""), "resource_marker": body.get("resource_marker", ""),
                "evaluation_http_status": body.get("evaluation_http_status", 0),
            })
            return {"ok": True}
        session = SESSIONS.get(token)
        if not session:
            return {"ok": False}
        return {"requests": json.loads(json.dumps(session["requests"]))}


def model(body, handler, messages):
    text = _text(messages)
    with LOCK:
        token = next((value for value in SESSIONS if value and value in text), None)
        if token is None:
            return False
        session = SESSIONS[token]
        session["requests"].append(body)
        tools = body.get("tools") or []
        tool_messages = [item for item in messages if item.get("role") == "tool"]
    if not tools:
        # Representative-task evaluation: no tools, no Run. The concrete output
        # is the expected result the client configured, so the pass is explicit.
        if session["evaluation_http_status"]:
            handler._json(session["evaluation_http_status"], {"error": {"message": "controlled evaluation outage"}})
            return True
        _emit(handler, {"role": "assistant", "content": session["evaluation"]}, "stop", body.get("stream"))
        return True
    names = [item.get("function", {}).get("name") for item in tools]
    if "load_skill" not in names or "read_skill_resource" not in names:
        handler._json(400, {"error": {"message": "frozen skill tools missing from the actual model request"}})
        return True
    loaded = next((item for item in tool_messages if item.get("name") == "load_skill"), None)
    if loaded is None:
        _emit(handler, _tool_call("skill-load-1", "load_skill", {"version_id": session["version_id"]}), "tool_calls", body.get("stream"))
        return True
    if session["body_marker"] not in str(loaded.get("content", "")):
        handler._json(400, {"error": {"message": "loaded skill body is not the frozen package content"}})
        return True
    if session["resource"]:
        read = next((item for item in tool_messages if item.get("name") == "read_skill_resource"), None)
        if read is None:
            _emit(handler, _tool_call("skill-read-1", "read_skill_resource",
                                      {"version_id": session["version_id"], "resource": session["resource"]}), "tool_calls", body.get("stream"))
            return True
        if session["resource_marker"] not in str(read.get("content", "")):
            handler._json(400, {"error": {"message": "referenced skill resource mismatch"}})
            return True
    _emit(handler, {"role": "assistant", "content": session["answer"]}, "stop", body.get("stream"))
    return True
