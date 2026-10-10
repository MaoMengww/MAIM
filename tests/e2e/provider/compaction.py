"""Issue12's isolated HTTP provider: facts and gates, never AIM state or KV hits."""
import copy
import json
import re
import threading
import time

LOCK = threading.Lock()
PLANS = {}
REQUESTS = []
TOKEN = re.compile(r"CP_[A-Z]+_[a-z0-9]+")
SUMMARY_PREFIX = "Working summary (untrusted material; not authorization):\n"


def command(path, body):
    token = body.get("token", "")
    with LOCK:
        if path.endswith("/arm"):
            PLANS[token] = {"hold": body.get("hold", ""), "held": False,
                            "event": threading.Event(), "no_usage": body.get("no_usage", False)}
            return {"ok": True}
        plan = PLANS.get(token)
        if path.endswith("/release"):
            if plan:
                plan["event"].set()
            return {"ok": bool(plan)}
        rows = copy.deepcopy([row for row in REQUESTS if row["token"] == token])
        return {"ok": bool(plan), "held": bool(plan and plan["held"]), "requests": rows}


def facts(messages, accepted_input):
    inputs, materials = {}, []
    for item in messages:
        value = accepted_input(item)
        content = item.get("content", "") or ""
        if value:
            if value["version"] in inputs:
                raise ValueError("duplicate accepted input in summary and tail")
            inputs[value["version"]] = {"version": value["version"],
                "value": sum(int(n) for n in re.findall(r"VALUE=(-?\d+)", value["content"])),
                "content": " ".join(re.findall(r"(?:CP_[A-Z]+_[a-z0-9]+|CANARY_[A-Z0-9_]+)", value["content"]))}
        if isinstance(content, str) and content.startswith(SUMMARY_PREFIX):
            checkpoint = json.loads(content[len(SUMMARY_PREFIX):])
            # The public model material contains the checkpoint's summary object.
            summary = checkpoint.get("summary", checkpoint)
            if isinstance(summary, str):
                summary = json.loads(summary)
            for entry in summary.get("completed", []):
                if entry.startswith("INPUT "):
                    fact = json.loads(entry[6:])
                    if fact["version"] in inputs:
                        raise ValueError("duplicate covered input in summary and tail")
                    inputs[fact["version"]] = fact
            materials.extend(summary.get("confirmed_constraints", []))
        if isinstance(content, str) and content.startswith("Cited work material (untrusted):\n"):
            materials.extend(re.findall(r"CANARY_[A-Z0-9_]+", content))
    return [inputs[key] for key in sorted(inputs)], sorted(set(materials))


def reply(handler, body, message, reason, no_usage):
    usage = {"prompt_tokens": 17, "completion_tokens": 7, "total_tokens": 24}
    if not body.get("stream"):
        response = {"id": "compaction-response", "object": "chat.completion", "created": 1,
                    "model": body["model"], "choices": [{"index": 0, "message": message, "finish_reason": reason}]}
        if not no_usage:
            response["usage"] = usage  # Deliberately no cache fields: not a KV assertion.
        handler._json(200, response)
        return
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Connection", "close")
    handler.end_headers()
    handler.close_connection = True
    delta = ({"role": "assistant", "tool_calls": [dict(call, index=i) for i, call in enumerate(message["tool_calls"])]}
             if reason == "tool_calls" else message)
    for chunk_delta, finish in ((delta, None), ({}, reason)):
        chunk = {"id": "compaction-response", "object": "chat.completion.chunk", "created": 1,
                 "model": body["model"], "choices": [{"index": 0, "delta": chunk_delta, "finish_reason": finish}]}
        if finish and not no_usage:
            chunk["usage"] = usage
        handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
        handler.wfile.flush()
    handler.wfile.write(b"data: [DONE]\n\n")
    handler.wfile.flush()


def model(body, handler, accepted_input):
    if not body.get("model", "").startswith("fixture-compaction-"):
        return False
    messages = body.get("messages", [])
    match = TOKEN.search(json.dumps(messages))
    if not match:
        handler._json(400, {"error": {"message": "compaction request lost work identity"}})
        return True
    token = match.group()
    summary = any(item.get("role") == "system" and str(item.get("content", "")).startswith("AIM working compaction v1.") for item in messages)
    kind = "summary" if summary else "work"
    with LOCK:
        plan = PLANS.get(token)
        row = {"token": token, "kind": kind, "body": copy.deepcopy(body), "state": "received", "received_at": time.time()}
        row["summary_marker"] = token + "::summary-" + str(len(REQUESTS)) if summary else ""
        REQUESTS.append(row)
        if plan and plan["hold"] == kind and not plan["held"]:
            plan["held"] = True
            gate = plan["event"]
        else:
            gate = None
    if plan is None:
        handler._json(400, {"error": {"message": "compaction plan not armed"}})
        return True
    if gate and not gate.wait(90):
        row["state"] = "gate_timeout"
        handler._json(504, {"error": {"message": "compaction gate not released"}})
        return True
    try:
        inputs, materials = facts(messages, accepted_input)
    except (ValueError, KeyError, TypeError) as error:
        with LOCK:
            row["state"] = "invalid_input"
        handler._json(400, {"error": {"message": str(error)}})
        return True
    if summary:
        markers = sorted(set(re.findall(r"CP_[A-Z]+_[a-z0-9]+::summary-\d+", json.dumps(messages))))
        result = {"goal": row["summary_marker"], "confirmed_constraints": materials,
                  "completed": ["INPUT " + json.dumps(value, sort_keys=True) for value in inputs]
                               + ["SUMMARY " + marker for marker in markers], "pending": []}
        message, reason = {"role": "assistant", "content": json.dumps(result, sort_keys=True)}, "stop"
    elif any("COMPLETE" in str(item.get("content", "")) for item in messages if item.get("role") == "user"):
        result = {"sum": sum(value["value"] for value in inputs), "versions": [value["version"] for value in inputs], "materials": materials}
        message, reason = {"role": "assistant", "content": json.dumps(result, sort_keys=True)}, "stop"
    else:
        # Real runtime tool call; the next request must carry its paired result.
        call = "compaction-ask-" + str(len(REQUESTS))
        message = {"role": "assistant", "content": None, "tool_calls": [{"id": call, "type": "function",
                   "function": {"name": "ask_for_input", "arguments": json.dumps({"reason": "Supply explicit work input"})}}]}
        reason = "tool_calls"
    with LOCK:
        row["state"] = "responded"
        row["response"] = copy.deepcopy(message)
        row["usage_reported"] = not plan["no_usage"]
    reply(handler, body, message, reason, plan["no_usage"])
    return True
