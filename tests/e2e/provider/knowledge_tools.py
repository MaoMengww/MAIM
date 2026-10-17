"""Controlled external reasoning; answers are derived only from AIM tool output."""
import json
import threading

LOCK = threading.Lock()
PLANS = {}


def command(path, body):
    token = body.get("token", "")
    with LOCK:
        if path.endswith("/arm"):
            PLANS[token] = {"calls": body.get("calls") or [], "hold_at": body.get("hold_at", -1),
                            "held": False, "event": threading.Event(), "requests": [], "embeddings": [],
                            "ask": body.get("ask", False)}
            return {"ok": True}
        plan = PLANS.get(token)
        if path.endswith("/release"):
            if plan:
                plan["event"].set()
            return {"ok": bool(plan)}
        return {"held": bool(plan and plan["held"]), "requests": plan["requests"] if plan else [],
                "embeddings": plan["embeddings"] if plan else []}


def embedding(body, handler):
    model = body.get("model", "")
    if not model.startswith("fixture-embed-knowledge-tools-"):
        return False
    inputs = body.get("input", [])
    if isinstance(inputs, str):
        inputs = [inputs]
    with LOCK:
        for token, plan in PLANS.items():
            if any(token in value for value in inputs):
                plan["embeddings"].extend(inputs)
    if any("KNOWLEDGE_SERVICE_FAIL" in value for value in inputs):
        handler._json(503, {"error": {"message": "Bearer secret-knowledge-provider-credential unavailable"}})
        return True
    return False


def results(messages):
    output = []
    for item in messages:
        if item.get("role") != "tool":
            continue
        try:
            value = json.loads(item.get("content", ""))
        except (ValueError, TypeError):
            continue
        if isinstance(value, dict) and value.get("status") in ("ok", "error"):
            value["_tool"] = item.get("name", "search_knowledge")
            output.append(value)
    return output


def send(body, handler, message, reason):
    if not body.get("stream"):
        handler._json(200, {"id": "knowledge-response", "object": "chat.completion", "created": 1,
                          "model": body["model"], "choices": [{"index": 0, "message": message, "finish_reason": reason}],
                          "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
        return
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Connection", "close")
    handler.end_headers()
    handler.close_connection = True
    delta = ({"role": "assistant", "tool_calls": [dict(call, index=i) for i, call in enumerate(message["tool_calls"])]}
             if reason == "tool_calls" else message)
    for payload, finish in ((delta, None), ({}, reason)):
        chunk = {"id": "knowledge-response", "object": "chat.completion.chunk", "created": 1,
                 "model": body["model"], "choices": [{"index": 0, "delta": payload, "finish_reason": finish}]}
        if finish:
            chunk["usage"] = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
        handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
        handler.wfile.flush()
    handler.wfile.write(b"data: [DONE]\n\n")
    handler.wfile.flush()


def model(body, handler, accepted_input):
    if not body.get("model", "").startswith("fixture-knowledge-tools-"):
        return False
    messages = body.get("messages", [])
    inputs = [value for item in messages if (value := accepted_input(item)) is not None]
    token = inputs[0]["content"] if inputs else ""
    with LOCK:
        token = next((value["content"] for value in reversed(inputs) if value["content"] in PLANS), token)
    with LOCK:
        plan = PLANS.get(token)
        if plan:
            plan["requests"].append(body)
            found = results(messages)
            index = len(found)
            gate = plan["event"] if index == plan["hold_at"] and not plan["held"] else None
            if gate:
                plan["held"] = True
    if plan is None:
        handler._json(400, {"error": {"message": "knowledge reasoning plan not armed"}})
        return True
    if gate and not gate.wait(180):
        handler._json(504, {"error": {"message": "knowledge reasoning gate not released"}})
        return True
    names = [item.get("function", {}).get("name") for item in body.get("tools", [])]
    if plan["ask"] and len(inputs) == 1 and names:
        name, arguments = "ask_for_input", {"reason": "Provide the deployment plan and constraints"}
    elif index < len(plan["calls"]) and names:
        call = plan["calls"][index]
        arguments = dict(call)
        name = arguments.pop("tool", "search_knowledge")
        if arguments.pop("continue_catalog", False):
            previous = next((value for value in reversed(found) if value["_tool"] == "list_knowledge_documents" and value.get("status") == "ok"), None)
            if previous is None or not previous.get("has_more"):
                handler._json(400, {"error": {"message": "catalog continuation requires a real preceding page"}})
                return True
            arguments["offset"] = previous["next_offset"]
        if arguments.pop("continue_document", False):
            previous = next((value for value in reversed(found) if value["_tool"] == "read_knowledge_document" and value.get("status") == "ok"), None)
            if previous is None or not previous.get("has_more"):
                handler._json(400, {"error": {"message": "document continuation requires a real preceding page"}})
                return True
            arguments["offset"] = previous["next_offset"]
        if arguments.pop("continue_history", False):
            previous = next((value for value in reversed(found) if value["_tool"] == "read_conversation_history" and value.get("status") == "ok"), None)
            if previous is None or not previous.get("has_more"):
                handler._json(400, {"error": {"message": "history continuation requires a real preceding page"}})
                return True
            arguments["cursor"] = previous["next_cursor"]
        if arguments.pop("from_input", False):
            arguments["query"] = inputs[-1]["content"]
        if arguments.pop("from_context", False):
            background = next((str(item.get("content", "")) for item in messages
                               if "P5_KNOWN_ANCHOR" in str(item.get("content", "")) and item.get("role") != "tool"), "")
            if not background:
                handler._json(400, {"error": {"message": "accepted parent deployment background missing"}})
                return True
            arguments["query"] = "P5_KNOWN_ANCHOR deployment 单写 双活"
    else:
        # No answer facts are kept in the plan. Only real tool payloads can
        # produce evidence; no tools means bounded delivery, not another call.
        items = [item for value in found if value.get("status") == "ok" and value["_tool"] == "search_knowledge"
                 for item in value.get("items", [])]
        discoveries = [value for value in found if value.get("status") == "ok" and value["_tool"] == "list_knowledge_bases"]
        document_pages = [value for value in found if value.get("status") == "ok" and value["_tool"] == "list_knowledge_documents"]
        document_reads = [value for value in found if value.get("status") == "ok" and value["_tool"] == "read_knowledge_document"]
        history_reads = [value for value in found if value.get("status") == "ok" and value["_tool"] == "read_conversation_history"]
        answer = {"evidence": items, "discoveries": discoveries,
                  "document_pages": document_pages, "document_reads": document_reads, "history_reads": history_reads,
                  "errors": [value["error"] for value in found if value.get("status") == "error"],
                  "gap": not bool(items or document_pages or document_reads or history_reads), "bounded": index < len(plan["calls"]),
                  "accepted_inputs": [value["content"] for value in inputs]}
        send(body, handler, {"role": "assistant", "content": json.dumps(answer, ensure_ascii=False)}, "stop")
        return True
    send(body, handler, {"role": "assistant", "content": None, "tool_calls": [{"id": "knowledge-call-" + str(index) + "-" + str(len(inputs)),
         "type": "function", "function": {"name": name, "arguments": json.dumps(arguments)}}]}, "tool_calls")
    return True
