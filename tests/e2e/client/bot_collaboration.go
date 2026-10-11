package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type collaborationOrigin struct {
	Kind                string   `json:"kind"`
	RuleID              entityID `json:"rule_id"`
	RuleVersion         int64    `json:"rule_version"`
	AuthorizationUserID entityID `json:"authorization_user_id"`
	ActivityID          string   `json:"activity_id"`
	SourceRunID         entityID `json:"source_run_id"`
	DelegatingBotID     entityID `json:"delegating_bot_id"`
}

// This journey observes only the public control/runtime contracts and actual
// executor deliveries. Neither database state nor implementation wiring is proof.
func (d *driver) botCollaboration(address, secondAddress string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("collab_owner", suffix)
	if err != nil {
		return err
	}
	admin, err := d.register("collab_admin", suffix)
	if err != nil {
		return err
	}
	requester, err := d.register("collab_requester", suffix)
	if err != nil {
		return err
	}
	outsider, err := d.register("collab_outsider", suffix)
	if err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", admin.token, map[string]any{"type": "group", "group_name": "collaboration-" + suffix, "member_ids": []entityID{requester.id}}, &conv); err != nil {
		return err
	}
	if d.controlDir != "" {
		raw, err := json.Marshal(map[string]any{"admin": map[string]string{"username": admin.username, "password": admin.password}, "conv_id": conv.ID})
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(d.controlDir, "collaboration-login.json"), raw, 0600); err != nil {
			return err
		}
	}
	var bots [5]botView
	var executors [5]*controlledExecutor
	for i := range bots {
		if err = d.request(http.MethodPost, "/bots", owner.token, map[string]any{"name": fmt.Sprintf("collaboration-%d-%s", i, suffix), "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "capabilities": `{"input_types":["text"],"supports_input":true,"supports_stop":true,"supports_resume":true}`}, &bots[i]); err != nil {
			return err
		}
		if err = d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", admin.token, map[string]any{"bot_id": bots[i].ID}, nil); err != nil {
			return err
		}
		executors[i], err = d.websocketExecutor(owner, bots[i].ID, address, fmt.Sprintf("collab-%d-%s", i, suffix))
		if err != nil {
			return err
		}
		defer executors[i].conn.Close()
	}
	// Sentinel observes the same ordered partition, making negative assertions
	// consumer-barrier-based instead of sleeps or arbitrary polling counts.
	if err = d.activitySettings(admin, conv.ID, bots[3].ID, `{"activity_types":["message.created"]}`); err != nil {
		return err
	}
	sentinel := activityObserver{x: executors[3]}
	send := func(text string) (sentMessage, error) {
		source, e := d.sendMessage(requester, conv.ID, text, 0)
		if e != nil {
			return source, e
		}
		binding, e := d.activityBinding(admin, conv.ID, bots[3].ID)
		if e != nil {
			return source, e
		}
		return source, sentinel.wait(source, binding)
	}
	rule := uuid.NewString()
	settings := func(enabled bool, contains string) string {
		raw, _ := json.Marshal(map[string]any{"activity_types": []string{"message.created"}, "response_rules": []map[string]any{{"id": rule, "enabled": enabled, "activity_type": "message.created", "message_contains": contains}}})
		return string(raw)
	}
	if err = d.activitySettings(admin, conv.ID, bots[0].ID, settings(false, "COLLAB_MATCH")); err != nil {
		return err
	}
	if _, err = send("COLLAB_MATCH disabled " + suffix); err != nil {
		return err
	}
	if err = d.activityNoWork(requester, conv.ID); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPut, "/convs/"+conv.ID.String()+"/bots/"+bots[0].ID.String(), requester, map[string]string{"bot_settings": settings(true, "COLLAB_MATCH")}, http.StatusForbidden); err != nil {
		return err
	}
	if err = d.activitySettings(admin, conv.ID, bots[0].ID, settings(true, "COLLAB_MATCH")); err != nil {
		return err
	}
	if _, err = send("ordinary unrelated conversation " + suffix); err != nil {
		return err
	}
	if err = d.activityNoWork(requester, conv.ID); err != nil {
		return err
	}
	source, err := send("COLLAB_MATCH count these four words " + suffix)
	if err != nil {
		return err
	}
	var automatic publicRun
	if err = d.broadcastPoll("explicit rule Run", func(bounded *driver) (bool, error) {
		runs, e := bounded.conversationRuns(requester, conv.ID)
		if e != nil {
			return false, e
		}
		for _, run := range runs {
			if run.Activity != nil && run.Activity.MessageID == source.MessageID {
				automatic = run
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	if automatic.TriggerKind != "rule" || automatic.RequesterID != "" || automatic.Origin == nil || automatic.Origin.Kind != "rule" || automatic.Origin.RuleID.String() != rule || automatic.Origin.AuthorizationUserID != admin.id || automatic.Origin.RuleVersion < 1 {
		return errors.New("rule Run lost actual rule authority or fabricated requester")
	}
	request, err := executors[0].request(automatic.ID)
	if err != nil {
		return err
	}
	if !strings.Contains(request.Text(), source.Content.Text) {
		return errors.New("rule accepted input not delivered to actual executor")
	}
	repeated, err := d.sendMessageWithKey(requester, conv.ID, source.Content.Text, 0, source.SubmissionKey)
	if err != nil {
		return err
	}
	if repeated.MessageID != source.MessageID {
		return errors.New("activity retry changed source identity")
	}
	if _, err = send("ordinary repeated delivery barrier " + suffix); err != nil {
		return err
	}
	acceptedRuns, err := d.conversationRuns(admin, conv.ID)
	if err != nil {
		return err
	}
	if len(acceptedRuns) != 1 || acceptedRuns[0].ID != automatic.ID {
		return errors.New("repeated activity created duplicate rule work")
	}
	// Original message edits do not mutate this already accepted work.
	if err = d.mutateWorkSource(requester, admin, source, "edit"); err != nil {
		return err
	}
	if _, err = send("ordinary consumer barrier after edit " + suffix); err != nil {
		return err
	}
	reread, err := d.readRun(admin, automatic.ID)
	if err != nil {
		return err
	}
	if reread.InputVersion != automatic.InputVersion || reread.StopStatus != "none" {
		return errors.New("message operation modified rule work")
	}
	if _, err = executors[0].update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	result := executorPayload(request, "run.update", "completed", 1)
	result["result_text"] = executorText(request.Text())
	if _, err = executors[0].update(result, false, false); err != nil {
		return err
	}
	if _, err = d.publishedRun(admin, automatic.ID); err != nil {
		return err
	}
	if err = d.activitySettings(admin, conv.ID, bots[0].ID, settings(false, "COLLAB_MATCH")); err != nil {
		return err
	}
	if _, err = send("COLLAB_MATCH disabled again " + suffix); err != nil {
		return err
	}
	runs, err := d.conversationRuns(admin, conv.ID)
	if err != nil {
		return err
	}
	if len(runs) != 1 {
		return errors.New("disabled rules or ordinary Bot result recursively accepted work")
	}
	fmt.Println("E2E PASS: collaboration rules disabled/enabled/condition/current admin/source attribution/no fake requester/IM isolation/no Bot feedback loop")

	// Explicit delegation uses executor identity rather than forging a new user message.
	parent, err := d.startRun(requester, bots[0].ID, conv.ID, "delegate count task "+suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	parentRequest, err := executors[0].request(parent.ID)
	if err != nil {
		return err
	}
	if _, err = executors[0].update(executorPayload(parentRequest, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	delegate := func(x *controlledExecutor, p publicRun, target entityID, key, text string) (publicRun, error) {
		var child publicRun
		e := d.request(http.MethodPost, "/bot-app/delegations", x.token, map[string]any{"operation": "delegate", "run_id": p.ID, "generation": p.Generation, "input_version": p.InputVersion, "target_bot_id": target, "submission_key": key, "message": text}, &child)
		return child, e
	}
	key := uuid.NewString()
	child, err := delegate(executors[0], parent, bots[1].ID, key, "count five explicit task words")
	if err != nil {
		return fmt.Errorf("explicit delegation: %w", err)
	}
	if child.RequesterID != "" || child.TriggerKind != "delegation" || child.Origin == nil || child.Origin.SourceRunID != parent.ID || child.Origin.DelegatingBotID != bots[0].ID || child.BotID != bots[1].ID || child.ID == parent.ID {
		return errors.New("delegation identity/source contract lost or requester forged")
	}
	replay, err := delegate(executors[0], parent, bots[1].ID, key, "count five explicit task words")
	if err != nil {
		return err
	}
	if replay.ID != child.ID {
		return errors.New("delegation retry duplicated work")
	}
	if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: executors[0].token}, map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "target_bot_id": bots[1].ID, "submission_key": key, "message": "changed task"}, http.StatusConflict); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodGet, "/runs/"+child.ID.String(), outsider, nil, http.StatusForbidden); err != nil {
		return err
	}
	cr, err := executors[1].request(child.ID)
	if err != nil {
		return err
	}
	if cr.Text() != "count five explicit task words" {
		return errors.New("delegate context expanded beyond explicit task")
	}
	if _, err = executors[1].update(executorPayload(cr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	finish := executorPayload(cr, "run.update", "completed", 1)
	finish["result_text"] = "5"
	if _, err = executors[1].update(finish, false, false); err != nil {
		return err
	}
	if _, err = d.publishedRun(admin, child.ID); err != nil {
		return err
	}
	var consumed struct {
		Run        publicRun `json:"run"`
		ResultText string    `json:"result_text"`
	}
	if err = d.request(http.MethodPost, "/bot-app/delegations", executors[0].token, map[string]any{"operation": "result", "run_id": parent.ID, "generation": parent.Generation, "input_version": parent.InputVersion, "child_run_id": child.ID}, &consumed); err != nil {
		return err
	}
	if consumed.Run.ID != child.ID || consumed.ResultText != "5" {
		return errors.New("parent cannot explicitly consume independent child result")
	}
	// Another live child then a grandchild demonstrate bounded recursion and
	// independent actual stop outcomes, not atomic cancellation claims.
	live, err := delegate(executors[0], parent, bots[1].ID, uuid.NewString(), "live delegated task")
	if err != nil {
		return err
	}
	lr, err := executors[1].request(live.ID)
	if err != nil {
		return err
	}
	if _, err = executors[1].update(executorPayload(lr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	grand, err := delegate(executors[1], live, bots[2].ID, uuid.NewString(), "grandchild delegated task")
	if err != nil {
		return err
	}
	gr, err := executors[2].request(grand.ID)
	if err != nil {
		return err
	}
	if _, err = executors[2].update(executorPayload(gr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: executors[2].token}, map[string]any{"operation": "delegate", "run_id": grand.ID, "generation": grand.Generation, "input_version": grand.InputVersion, "target_bot_id": bots[0].ID, "submission_key": uuid.NewString(), "message": "ancestor cycle"}, http.StatusTooManyRequests); err != nil {
		return fmt.Errorf("cycle limit: %w", err)
	}
	deepest, err := delegate(executors[2], grand, bots[3].ID, uuid.NewString(), "third depth independent child")
	if err != nil {
		return err
	}
	deepestRequest, err := executors[3].request(deepest.ID)
	if err != nil {
		return err
	}
	if _, err = executors[3].update(executorPayload(deepestRequest, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/bot-app/delegations", account{token: executors[3].token}, map[string]any{"operation": "delegate", "run_id": deepest.ID, "generation": deepest.Generation, "input_version": deepest.InputVersion, "target_bot_id": bots[4].ID, "submission_key": uuid.NewString(), "message": "depth exhausted"}, http.StatusTooManyRequests); err != nil {
		return fmt.Errorf("depth limit: %w", err)
	}
	stopping, err := d.controlExecutorRun(requester, parent.ID, "stop", "", uuid.NewString())
	if err != nil {
		return err
	}
	if stopping.StopStatus != "requested" {
		return errors.New("parent stop falsely confirmed")
	}
	for i, item := range []struct {
		x *controlledExecutor
		r publicRun
	}{{executors[0], parent}, {executors[1], live}, {executors[2], grand}} {
		sr, e := item.x.requestVersion(item.r.ID, item.r.Generation, item.r.InputVersion, "stopping")
		if e != nil {
			return e
		}
		state := "stopped"
		if i == 2 {
			state = "stop_rejected"
		}
		if _, e = item.x.update(executorPayload(sr, "run.update", state, 1), false, false); e != nil {
			return e
		}
	}
	stopped, err := d.readRun(admin, parent.ID)
	if err != nil {
		return err
	}
	stoppedChild, err := d.readRun(admin, live.ID)
	if err != nil {
		return err
	}
	refused, err := d.readRun(admin, grand.ID)
	if err != nil {
		return err
	}
	if stopped.StopStatus != "confirmed" || stoppedChild.StopStatus != "confirmed" || refused.StopStatus != "rejected" {
		return errors.New("associated stops hid independent executor outcomes")
	}
	if _, err = executors[3].requestVersion(deepest.ID, deepest.Generation, deepest.InputVersion, "stopping"); err != nil {
		return err
	}
	unknown, err := d.controlRun(admin, deepest.ID, func(r publicRun) bool { return r.StopStatus == "unknown" })
	if err != nil {
		return err
	}
	if unknown.Status != "stopping" {
		return errors.New("unconfirmed child falsely marked stopped")
	}
	if err = d.collaborationBounds(requester, conv.ID, bots, executors[0]); err != nil {
		return err
	}
	if err = d.collaborationResourceBoundary(admin, requester, conv.ID, bots, executors[0], suffix); err != nil {
		return err
	}
	if err = d.collaborationActivities(admin, requester, outsider, conv.ID, bots[0], executors[0]); err != nil {
		return err
	}
	if err = d.collaborationBuiltin(owner, admin, requester, conv.ID, bots[1], executors[1], suffix); err != nil {
		return err
	}
	if err = d.collaborationTerminalReplay(requester, conv.ID, bots, executors[0], executors[1]); err != nil {
		return err
	}
	if err = d.collaborationRevokedResult(admin, requester, conv.ID, bots, executors[0], executors[1], suffix); err != nil {
		return err
	}
	if err = d.collaborationSourceGrant(admin, requester, conv.ID, bots, executors[0], executors[1], suffix); err != nil {
		return err
	}
	if err = d.collaborationRejoin(admin, requester, conv.ID, bots, executors[0], executors[4]); err != nil {
		return err
	}
	if d.controlDir != "" {
		fixture := map[string]any{"admin": map[string]any{"username": admin.username, "password": admin.password}, "conv_id": conv.ID, "rule_id": rule, "rule_run_id": automatic.ID, "parent_run_id": parent.ID, "child_run_id": live.ID, "grandchild_run_id": grand.ID, "unknown_run_id": deepest.ID, "bot_ids": bots}
		raw, e := json.Marshal(fixture)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(d.controlDir, "collaboration-browser.json"), raw, 0600); e != nil {
			return e
		}
	}
	fmt.Println("E2E PASS: collaboration independent child identity/minimal context/idempotency/conflict/explicit result/cycle constraint/linked accepted-confirmed-refused stops")
	return nil
}
