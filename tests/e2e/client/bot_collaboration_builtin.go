package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
)

func (d *driver) collaborationBuiltin(owner, admin, requester account, conv entityID, target botView, x *controlledExecutor, suffix string) error {
	model, err := d.fixtureModel(owner, "chat", "fixture-collaboration-delegate-"+suffix)
	if err != nil {
		return err
	}
	var builtin botView
	if err = d.request(http.MethodPost, "/bots", owner.token, map[string]any{"name": "collaboration-builtin-" + suffix, "type": "self_deployed", "model_id": model, "streaming_enabled": false, "enable_knowledge": false}, &builtin); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", admin.token, map[string]any{"bot_id": builtin.ID}, nil); err != nil {
		return err
	}
	task, _ := json.Marshal(map[string]any{"target_bot_id": target.ID, "submission_key": uuid.NewString(), "message": "independent builtin delegated word count"})
	parent, err := d.startRun(requester, builtin.ID, conv, string(task), uuid.NewString(), nil)
	if err != nil {
		return err
	}
	paused, err := d.interactionWait(requester, parent.ID, "waiting_input")
	if err != nil {
		return err
	}
	if len(paused.ChildRunIDs) != 1 {
		return errors.New("actual builtin delegate_run failed to create independent child")
	}
	req, err := x.request(paused.ChildRunIDs[0])
	if err != nil {
		return err
	}
	if req.Text() != "independent builtin delegated word count" || req.Run.RequesterID != "" || req.Run.Origin == nil || req.Run.Origin.DelegatingBotID != builtin.ID {
		return errors.New("builtin child inherited private context or lost Bot identity")
	}
	if _, err = x.update(executorPayload(req, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	done := executorPayload(req, "run.update", "completed", 1)
	done["result_text"] = executorText(req.Text())
	if _, err = x.update(done, false, false); err != nil {
		return err
	}
	if _, err = d.publishedRun(admin, req.Run.ID); err != nil {
		return err
	}
	if _, err = d.interactionControl(requester, parent.ID, "input", "The child completed; consume its result.", uuid.NewString(), nil); err != nil {
		return err
	}
	completed, err := d.interactionWait(requester, parent.ID, "succeeded")
	if err != nil {
		return err
	}
	if completed.ResultText != "parent-consumed:"+executorText(req.Text()) {
		return fmt.Errorf("actual builtin delegation_result did not use real independent child result: %s", completed.ResultText)
	}
	if _, err = d.publishedRun(admin, parent.ID); err != nil {
		return err
	}
	// The same automatic-rule input contract executes builtin work without a
	// fabricated requester; its model observes only accepted activity input.
	ruleModel, err := d.fixtureModel(owner, "chat", "fixture-collaboration-rule-"+suffix)
	if err != nil {
		return err
	}
	if err = d.request(http.MethodPut, "/bots/"+builtin.ID.String(), owner.token, map[string]any{"model_id": ruleModel}, nil); err != nil {
		return err
	}
	continued, err := d.interactionStart(requester, builtin.ID, conv, "FOLLOWUP_CONSUMED_CHILD", map[string]any{"parent_run_id": parent.ID})
	if err != nil {
		return fmt.Errorf("continue consumed child result: %w", err)
	}
	if continued.InteractionID != parent.InteractionID || continued.ParentRunID == nil || *continued.ParentRunID != parent.ID {
		return errors.New("continuation of consumed child lost explicit interaction relation")
	}
	if _, err = d.interactionWait(requester, continued.ID, "succeeded"); err != nil {
		return err
	}
	if _, err = d.publishedRun(admin, continued.ID); err != nil {
		return err
	}
	policy, _ := json.Marshal(map[string]any{"activity_types": []string{"message.created"}, "response_rules": []map[string]any{{"id": uuid.NewString(), "enabled": true, "activity_type": "message.created", "message_contains": "BUILTIN_RULE_MATCH"}}})
	if err = d.activitySettings(admin, conv, builtin.ID, string(policy)); err != nil {
		return err
	}
	source, err := d.sendMessage(requester, conv, "BUILTIN_RULE_MATCH exact accepted input", 0)
	if err != nil {
		return err
	}
	var rule publicRun
	if err = d.broadcastPoll("builtin automatic rule result", func(b *driver) (bool, error) {
		runs, e := b.conversationRuns(admin, conv)
		if e != nil {
			return false, e
		}
		for _, r := range runs {
			if r.BotID == builtin.ID && r.Activity != nil && r.Activity.MessageID == source.MessageID {
				rule = r
				return r.Status == "succeeded" && r.OutputStatus == "committed", nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	if rule.RequesterID != "" || rule.Origin == nil || rule.ResultText != "rule-observed:"+source.Content.Text {
		return errors.New("builtin automatic execution bypassed actual input/origin contract")
	}
	if err = d.activitySettings(admin, conv, builtin.ID, `{"activity_types":[],"response_rules":[]}`); err != nil {
		return err
	}
	fmt.Println("E2E PASS: actual Eino delegate_run -> independent third-party child -> wait input -> delegation_result -> common output; builtin rule accepted activity input without fake requester")
	return nil
}
