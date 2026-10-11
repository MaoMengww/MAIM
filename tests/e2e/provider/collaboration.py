"""Deterministic external model protocol for real platform collaboration tools."""
import json


def reply(body, accepted_input):
    inputs = [value for item in body.get("messages", [])
              if (value := accepted_input(item)) is not None]
    if not inputs:
        raise ValueError("collaboration model received no accepted input")
    tools = {item.get("tool_call_id"): item for item in body.get("messages", [])
             if item.get("role") == "tool"}
    if body["model"].startswith("fixture-collaboration-rule-"):
        return {"role": "assistant", "content": "rule-observed:" + inputs[0]["content"]}, "stop"
    task = json.loads(inputs[0]["content"])
    available = {item["function"]["name"] for item in body.get("tools", [])}
    if not {"delegate_run", "delegation_result", "ask_for_input"}.issubset(available):
        raise ValueError("real platform collaboration tools absent")

    def call(identity, name, arguments):
        return {"role": "assistant", "content": None, "tool_calls": [{
            "id": identity, "type": "function", "function": {
                "name": name, "arguments": json.dumps(arguments)}}]}, "tool_calls"

    if "collaboration-delegate" not in tools:
        return call("collaboration-delegate", "delegate_run", {
            "target_bot_id": task["target_bot_id"], "submission_key": task["submission_key"],
            "message": task["message"]})
    child = json.loads(tools["collaboration-delegate"]["content"])
    if "id" not in child or child.get("requester_id"):
        raise ValueError("actual delegated child identity missing or human forged")
    if len(inputs) == 1:
        return call("collaboration-wait", "ask_for_input", {
            "reason": "Wait for the independently executing child then explicitly resume."})
    if "collaboration-result" not in tools:
        return call("collaboration-result", "delegation_result", {"child_run_id": child["id"]})
    result = json.loads(tools["collaboration-result"]["content"])
    if result.get("run", {}).get("status") != "succeeded":
        raise ValueError("real independent child has not finished")
    return {"role": "assistant", "content": "parent-consumed:" + result["result_text"]}, "stop"
