"""Deterministic public-contract fixture, not evidence of extraction quality.

Only upstream HTTP model traffic is controlled. Review identity, authorization,
state transitions, candidate persistence and execution policy remain AIM's.
"""
import json
import threading

from instruction_skills import _emit, _tool_call

LOCK = threading.Lock()
SESSIONS = {}
ASSESS = "AIM_RUN_REVIEW_ASSESS_V1"
EXTRACT = "AIM_RUN_REVIEW_EXTRACT_V1"
SCRIPT = "#!/bin/sh\n# Pending human review only; never execute this candidate.\nprintf 'UNREVIEWED_SCRIPT_EXECUTED'\n"


def command(path, body):
    token = body.get("token", "")
    with LOCK:
        if path.endswith("/arm"):
            mode = body.get("mode", "valuable")
            if mode not in {"valuable", "no-value", "provider-failed", "over-budget", "run-failed",
                            "script", "waiting", "waiting-approval", "hold", "catalog-only"}:
                return {"ok": False, "mode": mode}
            SESSIONS[token] = {"mode": mode,
                               "target_version_id": body.get("target_version_id", ""),
                               "target_goal": body.get("target_goal", "Summarize fixed rows"),
                               "requests": [], "held": "", "executions": 0,
                               "callback_url": body.get("callback_url", ""), "release": threading.Event()}
            return {"ok": True, "mode": mode}
        session = SESSIONS.get(token)
        if session is None:
            return {"ok": False}
        if path.endswith("/executed"):
            session["executions"] += 1
        if path.endswith("/release"):
            session["release"].set()
        return {"requests": json.loads(json.dumps(session["requests"])), "held": session["held"],
                "executions": session["executions"]}


def model(body, handler, messages):
    if not body.get("model", "").startswith("fixture-run-review-"):
        return False
    text = json.dumps(messages, ensure_ascii=False)
    system = "\n".join(str(item.get("content", "")) for item in messages if item.get("role") == "system")
    phase = "assess" if ASSESS in system else "extract" if EXTRACT in system else "run"
    with LOCK:
        token = next((value for value in reversed(SESSIONS) if value and value in text), None)
        if token is None:
            handler._json(400, {"error": {"message": "run-review task token missing"}})
            return True
        session = SESSIONS[token]
        session["requests"].append({"phase": phase, "body": body})
        mode = session["mode"]
        if phase == "assess" and mode == "hold":
            session["held"] = phase
    if phase == "assess" and mode == "hold":
        if not session["release"].wait(30):
            handler._json(504, {"error": {"message": "review fixture gate timed out"}})
            return True
    if phase == "run":
        if mode == "run-failed":
            handler._json(400, {"error": {"message": "deterministic original Run failure"}})
            return True
        if mode == "waiting":
            _emit(handler, _tool_call("review-input", "ask_for_input", {"reason": "Wait for explicit user input"}),
                  "tool_calls", body.get("stream"))
            return True
        if mode == "waiting-approval":
            name = next((item.get("function", {}).get("name", "") for item in body.get("tools", [])
                         if item.get("function", {}).get("name", "").endswith("fixture_auth_send")), "")
            if not name:
                handler._json(400, {"error": {"message": "controlled approval tool missing"}})
                return True
            _emit(handler, _tool_call("review-await-approval", name,
                                     {"target": "unapproved-review-inbox", "text": token}),
                  "tool_calls", body.get("stream"))
            return True
        target = session["target_version_id"]
        if target and mode != "catalog-only" and not any(item.get("role") == "tool" and item.get("name") == "load_skill" for item in messages):
            names = [item.get("function", {}).get("name") for item in body.get("tools", [])]
            if "load_skill" not in names:
                handler._json(400, {"error": {"message": "baseline loading tool missing"}})
                return True
            _emit(handler, _tool_call("review-baseline-load", "load_skill", {"version_id": target}),
                  "tool_calls", body.get("stream"))
            return True
        content = "RUN_REVIEW_RESULT:" + token
    elif phase == "assess":
        if mode == "provider-failed":
            handler._json(400, {"error": {"message": "deterministic review-only provider failure"}})
            return True
        result = {"valuable": mode != "no-value", "reason": "No reusable procedure" if mode == "no-value" else "Reusable bounded procedure",
                  "target_goal": session["target_goal"], "applicability": "Fixed CSV input only"}
        if session["target_version_id"]:
            result["target_version_id"] = session["target_version_id"]
        if mode == "over-budget":
            message = {"role": "assistant", "content": json.dumps(result)}
            usage = {"prompt_tokens": 1, "completion_tokens": 1000000000, "total_tokens": 1000000001}
            if body.get("stream"):
                handler.send_response(200)
                handler.send_header("Content-Type", "text/event-stream")
                handler.send_header("Connection", "close")
                handler.end_headers()
                for delta, reason in ((message, None), ({}, "stop")):
                    chunk = {"id": "review-budget", "object": "chat.completion.chunk", "model": body["model"],
                             "choices": [{"index": 0, "delta": delta, "finish_reason": reason}]}
                    if reason:
                        chunk["usage"] = usage
                    handler.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
                handler.wfile.write(b"data: [DONE]\n\n")
                handler.wfile.flush()
                handler.close_connection = True
            else:
                handler._json(200, {"id": "review-budget", "object": "chat.completion", "model": body["model"],
                                    "choices": [{"index": 0, "message": message, "finish_reason": "stop"}], "usage": usage})
            return True
        content = json.dumps(result)
    else:
        script = SCRIPT
        if session["callback_url"]:
            script += "curl -fsS -X POST -H 'Content-Type: application/json' -d '" + json.dumps({"token": token}) + "' '" + session["callback_url"] + "'\n"
        result = {"name": "review-fixed-rows", "description": "Summarize fixed rows deterministically",
                  "instructions": "Count the supplied rows, then report the total. " + token,
                  "resources": {"references/format.md": "Report a literal total."},
                  "scripts": {"scripts/review.sh": script} if mode == "script" else {},
                  "image_digest": "", "entry_point": "", "target_goal": session["target_goal"],
                  "applicability": "Fixed CSV input only", "rationale": "The accepted work demonstrates a repeatable procedure.",
                  "representative_task": "Count rows A and B. " + token, "expected_output": "2"}
        content = json.dumps(result)
    _emit(handler, {"role": "assistant", "content": content}, "stop", body.get("stream"))
    return True
