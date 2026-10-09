package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const controlCapabilities = `{"input_types":["text"],"supports_input":true,"supports_stop":true,"supports_resume":true}`

type controlObservation struct {
	State        string            `json:"state"`
	Ticks        int64             `json:"ticks"`
	Calls        int               `json:"calls"`
	Digest       string            `json:"digest"`
	StopRequests []executorRequest `json:"stop_requests"`
	Updates      []struct {
		Payload  map[string]json.RawMessage `json:"payload"`
		Response struct {
			Status int `json:"status"`
		} `json:"response"`
	} `json:"updates"`
}

type controlFixture struct {
	d                                                   *driver
	owner, admin, requester, member, delegate, newcomer account
	conv                                                entityID
	builtin, external                                   botView
	model                                               entityID
	x                                                   *controlledExecutor
	botToken                                            string
	suffix                                              string
	browser                                             map[string]any
}

func (d *driver) controlObserve(token string) (controlObservation, error) {
	var observed controlObservation
	err := d.executorProvider("/run-control/observe", map[string]string{"token": token}, &observed)
	return observed, err
}

func (d *driver) controlGate(token, operation, mode, kind string) error {
	var result struct {
		OK bool `json:"ok"`
	}
	err := d.executorProvider("/run-control/"+operation, map[string]string{"token": token, "mode": mode, "kind": kind}, &result)
	if err != nil {
		return err
	}
	if !result.OK {
		return errors.New("run-controls: external work gate refused")
	}
	return nil
}

func (d *driver) controlObserved(token, state string) (controlObservation, error) {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		observed, err := d.controlObserve(token)
		if err != nil {
			return observed, err
		}
		if observed.State == state && (state != "running" || observed.Ticks > 1) {
			return observed, nil
		}
		if strings.HasSuffix(observed.State, "failed") || observed.State == "timed_out" {
			return observed, fmt.Errorf("run-controls: external work %s, expected %s", observed.State, state)
		}
		time.Sleep(30 * time.Millisecond)
	}
	return controlObservation{}, fmt.Errorf("run-controls: external %s not observed for %s", state, token)
}

func (d *driver) controlRun(caller account, id entityID, predicate func(publicRun) bool) (publicRun, error) {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		run, err := d.readRun(caller, id)
		if err != nil {
			return run, err
		}
		if predicate(run) {
			return run, nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return publicRun{}, errors.New("run-controls: authoritative state not observed")
}

func (f *controlFixture) save() error {
	if f.d.controlDir == "" {
		return errors.New("bot-run-controls requires -control-dir for browser fixtures")
	}
	raw, err := json.MarshalIndent(f.browser, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.d.controlDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.d.controlDir, "browser-fixtures.json"), append(raw, '\n'), 0600)
}

func (f *controlFixture) externalStart(label, mode string) (publicRun, string, error) {
	token := "CONTROL_WORK_" + label + "_" + f.suffix
	var armed struct {
		OK bool `json:"ok"`
	}
	if err := f.d.executorProvider("/run-control/arm", map[string]string{"token": token, "mode": mode, "kind": "executor", "bot_token": f.botToken}, &armed); err != nil {
		return publicRun{}, token, err
	}
	if !armed.OK {
		return publicRun{}, token, errors.New("run-controls: external work was not armed")
	}
	run, err := f.d.startRun(f.requester, f.external.ID, f.conv, token+" VALUE=5", uuid.NewString(), nil)
	if err != nil {
		return run, token, err
	}
	if _, err = f.d.controlObserved(token, "running"); err != nil {
		return run, token, err
	}
	run, err = f.d.readRun(f.admin, run.ID)
	return run, token, err
}

func (f *controlFixture) stop(caller account, run publicRun, token, key string) (publicRun, error) {
	stopping, err := f.d.controlExecutorRun(caller, run.ID, "stop", "", key)
	if err != nil {
		return stopping, err
	}
	if (stopping.StopStatus != "requested" && !(run.ExecutorType == "builtin" && stopping.StopStatus == "confirmed" && stopping.Status == "stopped")) || stopping.StreamStatus != "closed" || stopping.StopReason == "" {
		return stopping, errors.New("run-controls: accepted stop/closed stream falsely reported confirmed work cancellation")
	}
	return stopping, nil
}

func (f *controlFixture) confirmed(run publicRun, token string) error {
	before, err := f.d.controlObserved(token, "stopped")
	if err != nil {
		return err
	}
	stopped, err := f.d.controlRun(f.admin, run.ID, func(r publicRun) bool { return r.Status == "stopped" && r.StopStatus == "confirmed" })
	if err != nil {
		return err
	}
	if stopped.StreamStatus != "closed" || stopped.MessageID != nil {
		return errors.New("run-controls: confirmed stop published a result or reopened output")
	}
	time.Sleep(180 * time.Millisecond)
	after, err := f.d.controlObserve(token)
	if err != nil {
		return err
	}
	if after.Ticks != before.Ticks || before.Ticks < 2 || before.Digest == "" {
		return errors.New("run-controls: external computation continued after actual stop")
	}
	fmt.Printf("Run-control external fact: run=%s outcome=stopped ticks=%d digest=%s unchanged_after_confirm=true\n", run.ID, before.Ticks, before.Digest)
	return nil
}

func (d *driver) botRunControls(address, secondAddress string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := controlFixture{d: d, suffix: suffix}
	for _, item := range []struct {
		name  string
		value *account
	}{{"owner", &f.owner}, {"admin", &f.admin}, {"requester", &f.requester}, {"member", &f.member}, {"delegate", &f.delegate}, {"newcomer", &f.newcomer}} {
		*item.value, err = d.register("control_"+item.name, suffix)
		if err != nil {
			return err
		}
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{"type": "group", "group_name": "run-controls-" + suffix, "member_ids": []entityID{f.requester.id, f.member.id, f.delegate.id}}, &conv); err != nil {
		return err
	}
	f.conv = conv.ID
	f.model, err = d.fixtureModel(f.owner, "chat", "fixture-control-"+suffix)
	if err != nil {
		return err
	}
	var mcp struct {
		ID entityID `json:"id"`
	}
	// Streamable HTTP lets the external tool endpoint directly observe a cancelled
	// in-flight tool call rather than infer cancellation from the user's WS.
	if err = d.request(http.MethodPost, "/mcp-servers", f.owner.token, map[string]any{"name": "control-tools-" + suffix, "transport": "http-streamable", "url": d.provider + "/mcp/sse", "enabled": true, "advanced_config": `{"timeout":90,"retry_count":0}`}, &mcp); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "control-builtin-" + suffix, "type": "self_deployed", "model_id": f.model, "system_prompt": "Use fixture_echo exactly once.", "response_triggers": []string{"mention"}, "streaming_enabled": true, "enable_knowledge": false}, &f.builtin); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots/"+f.builtin.ID.String()+"/mcp-servers", f.owner.token, map[string]any{"mcp_server_id": mcp.ID}, nil); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "control-external-" + suffix, "type": "third_party", "sub_type": "webhook", "conn_mode": "webhook", "callback_url": d.provider + "/external-webhook", "response_triggers": []string{"mention"}, "capabilities": controlCapabilities}, &f.external); err != nil {
		return err
	}
	for _, bot := range []botView{f.builtin, f.external} {
		if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
			return err
		}
	}
	f.x, err = d.webhookExecutor(f.owner, f.external.ID)
	if err != nil {
		return err
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err = d.request(http.MethodPost, "/bots/"+f.external.ID.String()+"/token", f.owner.token, map[string]int{"ttl_seconds": 3600}, &issued); err != nil {
		return err
	}
	f.botToken = issued.Token
	accounts := map[string]any{}
	for _, item := range []struct {
		name  string
		value account
	}{{"owner", f.owner}, {"admin", f.admin}, {"requester", f.requester}, {"member", f.member}, {"delegate", f.delegate}, {"newcomer", f.newcomer}} {
		accounts[item.name] = map[string]any{"id": item.value.id, "username": item.value.username, "password": item.value.password, "token": item.value.token}
	}
	f.browser = map[string]any{"accounts": accounts, "conversation_id": f.conv, "builtin_bot_id": f.builtin.ID, "external_bot_id": f.external.ID, "model_id": f.model, "gateway": d.gateway, "realtime_a": address, "realtime_b": secondAddress, "provider": d.provider, "observations": "/observations", "control_observation": "POST /run-control/observe {token}", "executor_credentials": map[string]string{"bot_token": f.botToken, "webhook_secret": f.x.secret}, "runs": map[string]any{}}
	if err = f.save(); err != nil {
		return err
	}
	fmt.Printf("Run-control browser fixtures: %s; group=%s builtin=%s external=%s; credentials stay in private local JSON\n", filepath.Join(d.controlDir, "browser-fixtures.json"), f.conv, f.builtin.ID, f.external.ID)
	socket, err := d.p6Connect(secondAddress, f.member)
	if err != nil {
		return err
	}
	defer socket.close()
	if err = f.rights(socket); err != nil {
		return fmt.Errorf("current control rights: %w", err)
	}
	if err = f.outcomes(); err != nil {
		return fmt.Errorf("actual third-party outcomes: %w", err)
	}
	if err = f.builtinCancellation(); err != nil {
		return fmt.Errorf("builtin cancellation: %w", err)
	}
	if err = f.membership(); err != nil {
		return fmt.Errorf("controller membership: %w", err)
	}
	if err = f.botEligibility(); err != nil {
		return fmt.Errorf("current Bot eligibility: %w", err)
	}
	return f.browserWork()
}

func (f *controlFixture) rights(socket *p6Socket) error {
	d := f.d
	run, token, err := f.externalStart("rights", "confirm")
	if err != nil {
		return err
	}
	if run.RequesterID != f.requester.id || run.ControllerID != f.requester.id {
		return errors.New("run-controls: initial origin/controller missing")
	}
	for _, operation := range []string{"stop", "takeover", "grant_control", "revoke_control"} {
		body := map[string]any{"operation": operation, "submission_key": uuid.NewString()}
		if operation == "grant_control" || operation == "revoke_control" {
			body["target_user_id"] = f.delegate.id
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.member, body, http.StatusForbidden); err != nil {
			return err
		}
	}
	input, err := d.controlExecutorRun(f.member, run.ID, "input", "VALUE=7", uuid.NewString())
	if err != nil {
		return err
	}
	if input.InputVersion != run.InputVersion+1 || input.RequesterID != f.requester.id {
		return errors.New("run-controls: ordinary supplement changed origin or lost input")
	}
	if _, err = d.p6Wait(socket, 0, "control-input", func(event p6Event) bool {
		return event.Type == "bot.run.updated" && event.Run.ID == run.ID && event.Run.InputVersion == input.InputVersion
	}); err != nil {
		return err
	}
	grantKey := uuid.NewString()
	grant, err := d.interactionControl(f.requester, run.ID, "grant_control", "", grantKey, map[string]any{"target_user_id": f.delegate.id})
	if err != nil {
		return err
	}
	if !slices.Contains(grant.ControlUserIDs, f.delegate.id) {
		return errors.New("run-controls: explicit controller not visible")
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.requester, map[string]any{"operation": "grant_control", "submission_key": grantKey, "target_user_id": f.member.id}, http.StatusConflict); err != nil {
		return err
	}
	for _, body := range []map[string]any{{"operation": "stop", "target_user_id": f.delegate.id, "submission_key": uuid.NewString()}, {"operation": "grant_control", "target_user_id": f.owner.id, "submission_key": uuid.NewString()}} {
		status := http.StatusBadRequest
		if body["operation"] == "grant_control" {
			status = http.StatusForbidden
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.requester, body, status); err != nil {
			return err
		}
	}
	takeover, err := d.interactionControl(f.delegate, run.ID, "takeover", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if takeover.RequesterID != f.requester.id || takeover.ControllerID != f.delegate.id || len(takeover.ControlUserIDs) != 0 {
		return errors.New("run-controls: takeover rewrote origin or inherited prior explicit grants")
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.member, map[string]any{"operation": "stop", "submission_key": uuid.NewString()}, http.StatusForbidden); err != nil {
		return err
	}
	key := uuid.NewString()
	stopping, err := f.stop(f.delegate, run, token, key)
	if err != nil {
		return err
	}
	if err = f.confirmed(stopping, token); err != nil {
		return err
	}
	if _, err = d.controlExecutorRun(f.delegate, run.ID, "stop", "", key); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/kick", f.admin.token, map[string]any{"user_ids": []entityID{f.delegate.id}}, nil); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.delegate, map[string]any{"operation": "stop", "submission_key": key}, http.StatusForbidden); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/invite", f.admin.token, map[string]any{"user_ids": []entityID{f.delegate.id}}, nil); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.delegate, map[string]any{"operation": "stop", "submission_key": key}, http.StatusForbidden); err != nil {
		return err
	}
	// Grant revocation is immediately authoritative even for a replayed receipt.
	other, otherToken, err := f.externalStart("revoke", "confirm")
	if err != nil {
		return err
	}
	grantKey = uuid.NewString()
	if err = d.runtimeReject(http.MethodPost, "/runs/"+other.ID.String()+"/control", f.delegate, map[string]any{"operation": "grant_control", "submission_key": grantKey, "target_user_id": f.member.id}, http.StatusForbidden); err != nil {
		return err
	}
	if _, err = d.interactionControl(f.requester, other.ID, "grant_control", "", grantKey, map[string]any{"target_user_id": f.delegate.id}); err != nil {
		return err
	}
	if _, err = d.interactionControl(f.requester, other.ID, "revoke_control", "", uuid.NewString(), map[string]any{"target_user_id": f.delegate.id}); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+other.ID.String()+"/control", f.delegate, map[string]any{"operation": "stop", "submission_key": uuid.NewString()}, http.StatusForbidden); err != nil {
		return err
	}
	current, err := d.readRun(f.admin, other.ID)
	if err != nil {
		return err
	}
	if slices.Contains(current.ControlUserIDs, f.delegate.id) {
		return errors.New("run-controls: revoked explicit controller remained in authoritative view")
	}
	if _, err = f.stop(f.admin, other, otherToken, uuid.NewString()); err != nil {
		return err
	}
	if err = f.confirmed(other, otherToken); err != nil {
		return err
	}
	own, ownToken, err := f.externalStart("requester", "confirm")
	if err != nil {
		return err
	}
	if _, err = f.stop(f.requester, own, ownToken, uuid.NewString()); err != nil {
		return err
	}
	return f.confirmed(own, ownToken)
}

func (f *controlFixture) outcomes() error {
	for _, mode := range []string{"pending", "reject", "race"} {
		run, token, err := f.externalStart(mode, mode)
		if err != nil {
			return err
		}
		if _, err = f.stop(f.requester, run, token, uuid.NewString()); err != nil {
			return err
		}
		if mode == "pending" || mode == "reject" {
			before, err := f.d.controlObserve(token)
			if err != nil {
				return err
			}
			if mode == "reject" {
				if _, err = f.d.controlRun(f.admin, run.ID, func(r publicRun) bool { return r.StopStatus == "rejected" }); err != nil {
					return err
				}
			}
			time.Sleep(180 * time.Millisecond)
			after, err := f.d.controlObserve(token)
			if err != nil {
				return err
			}
			current, err := f.d.readRun(f.admin, run.ID)
			if err != nil {
				return err
			}
			if after.Ticks <= before.Ticks || current.Status == "stopped" || current.StopStatus == "confirmed" || current.StreamStatus != "closed" {
				return errors.New("run-controls: closed stream/unconfirmed stop invented work cancellation")
			}
			if mode == "pending" && current.StopStatus != "requested" && current.StopStatus != "unknown" {
				return errors.New("run-controls: unconfirmed stop lost pending/unknown status")
			}
			fmt.Printf("Run-control external fact: run=%s mode=%s ticks=%d→%d stop_status=%s stream_status=%s still_computing=true\n", run.ID, mode, before.Ticks, after.Ticks, current.StopStatus, current.StreamStatus)
			if mode == "pending" {
				unknown, err := f.d.controlRun(f.admin, run.ID, func(r publicRun) bool { return r.StopStatus == "unknown" })
				if err != nil {
					return err
				}
				working, err := f.d.controlObserve(token)
				if err != nil {
					return err
				}
				if unknown.Status == "stopped" || working.Ticks <= after.Ticks {
					return errors.New("run-controls: lost executor stop confirmation was fabricated")
				}
			}
			if err = f.d.controlGate(token, "finish", "", ""); err != nil {
				return err
			}
		}
		done, err := f.d.publishedRun(f.admin, run.ID)
		if err != nil {
			return err
		}
		if done.Status != "succeeded" || done.ExecutorStatus != "completed" || done.StopStatus == "confirmed" || done.StreamStatus != "closed" {
			return errors.New("run-controls: actual completion could not win unconfirmed stop")
		}
		var result struct {
			Sum    int    `json:"sum"`
			Ticks  int64  `json:"ticks"`
			Digest string `json:"digest"`
		}
		if err = json.Unmarshal([]byte(done.ResultText), &result); err != nil {
			return err
		}
		if result.Sum != 5 || result.Ticks < 2 || len(result.Digest) != 64 {
			return errors.New("run-controls: executor did not compute real accepted input work")
		}
		request, err := f.x.request(run.ID)
		if err != nil {
			return err
		}
		observed, err := f.d.controlObserved(token, "completed")
		if err != nil {
			return err
		}
		sequence := int64(len(observed.Updates) - 1)
		for _, status := range []string{"running", "stopped", "stop_rejected"} {
			if _, err = f.x.update(executorPayload(request, "run.update", status, sequence), true, false); err != nil {
				return err
			}
		}
		old := executorPayload(request, "run.update", "completed", sequence)
		old["generation"] = request.Generation + 1
		old["result_text"] = "forged completion"
		if _, err = f.x.update(old, true, false); err != nil {
			return err
		}
		after, err := f.d.readRun(f.admin, run.ID)
		if err != nil {
			return err
		}
		if after.Version != done.Version || after.Status != done.Status || !sameEntityID(after.MessageID, done.MessageID) {
			return errors.New("run-controls: terminal state changed under late/incorrect generation update")
		}
		f.browser["runs"].(map[string]any)[mode] = map[string]any{"id": run.ID, "token": token}
	}
	return f.save()
}

func (f *controlFixture) builtinCancellation() error {
	for _, kind := range []string{"model", "tool"} {
		token := "CONTROL_" + strings.ToUpper(kind) + "_" + f.suffix
		if err := f.d.controlGate(token, "arm", "", kind); err != nil {
			return err
		}
		run, err := f.d.startRun(f.requester, f.builtin.ID, f.conv, token, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		if _, err = f.d.controlObserved(token, "running"); err != nil {
			return err
		}
		before, err := f.d.observeProvider()
		if err != nil {
			return err
		}
		if _, err = f.stop(f.requester, run, token, uuid.NewString()); err != nil {
			return err
		}
		cancelled, err := f.d.controlObserved(token, "cancelled")
		if err != nil {
			return err
		}
		if _, err = f.d.controlRun(f.admin, run.ID, func(r publicRun) bool { return r.Status == "stopped" && r.StopStatus == "confirmed" }); err != nil {
			return err
		}
		if err = f.d.controlGate(token, "release", "", ""); err != nil {
			return err
		}
		time.Sleep(180 * time.Millisecond)
		after, err := f.d.observeProvider()
		if err != nil {
			return err
		}
		count := func(observed providerObservations) (int, int) {
			models, tools := 0, 0
			for _, request := range observed.ChatRequests {
				if strings.Contains(string(request["messages"]), token) {
					models++
				}
			}
			for _, text := range observed.ToolTexts {
				if strings.Contains(text, token) {
					tools++
				}
			}
			return models, tools
		}
		bm, bt := count(before)
		am, at := count(after)
		if am != bm || at != bt || kind == "model" && at != 0 || kind == "tool" && at != 1 {
			return errors.New("run-controls: builtin continued model/tool steps after external cancellation")
		}
		fmt.Printf("Run-control builtin fact: run=%s endpoint=%s cancelled=true computation_ticks=%d model_calls=%d tool_calls=%d no_subsequent_steps=true\n", run.ID, kind, cancelled.Ticks, am, at)
	}
	return nil
}

func (f *controlFixture) membership() error {
	d := f.d
	// Hold a real model request; removing its current controller must cancel it,
	// not allow the requester's captured membership to keep driving the Run.
	token := "CONTROL_MODEL_MEMBERSHIP_" + f.suffix
	if err := d.controlGate(token, "arm", "", "model"); err != nil {
		return err
	}
	run, err := d.startRun(f.requester, f.builtin.ID, f.conv, token+" VALUE=13", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if _, err = d.controlObserved(token, "running"); err != nil {
		return err
	}
	inputKey := uuid.NewString()
	if _, err = d.controlExecutorRun(f.requester, run.ID, "input", "VALUE=17", inputKey); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/kick", f.admin.token, map[string]any{"user_ids": []entityID{f.requester.id}}, nil); err != nil {
		return err
	}
	for _, check := range []struct {
		method, path string
		body         any
	}{{http.MethodGet, "/runs/" + run.ID.String(), nil}, {http.MethodGet, "/conversations/" + f.conv.String() + "/runs", nil}, {http.MethodPost, "/runs/" + run.ID.String() + "/control", map[string]any{"operation": "input", "message": "VALUE=17", "submission_key": inputKey}}, {http.MethodPost, "/runs/" + run.ID.String() + "/control", map[string]any{"operation": "stop", "submission_key": uuid.NewString()}}} {
		if err = d.runtimeReject(check.method, check.path, f.requester, check.body, http.StatusForbidden); err != nil {
			return err
		}
	}
	paused, err := d.controlRun(f.admin, run.ID, func(r publicRun) bool { return r.Status == "waiting_authorization" && r.Stage == "awaiting_takeover" })
	if err != nil {
		return err
	}
	if paused.RequesterID != f.requester.id || paused.ControllerID != f.requester.id {
		return errors.New("run-controls: departure erased provenance instead of awaiting explicit takeover")
	}
	if _, err = d.controlObserved(token, "cancelled"); err != nil {
		return err
	}
	// Returning to the group does not automatically resume the old controller's
	// work or restore an earlier membership incarnation's authority.
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/invite", f.admin.token, map[string]any{"user_ids": []entityID{f.requester.id}}, nil); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.requester, map[string]any{"operation": "stop", "submission_key": uuid.NewString()}, http.StatusForbidden); err != nil {
		return err
	}
	rejoined, err := d.readRun(f.admin, run.ID)
	if err != nil {
		return err
	}
	if rejoined.Status != "waiting_authorization" || rejoined.Stage != "awaiting_takeover" {
		return errors.New("run-controls: requester rejoin revived paused work without takeover")
	}
	if err = d.controlGate(token, "arm", "", "model"); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.member, map[string]any{"operation": "takeover", "submission_key": uuid.NewString()}, http.StatusForbidden); err != nil {
		return err
	}
	// Continuing shared work must not inherit a former requester's private file.
	private, err := d.attachmentUpload(f.requester, "takeover-private-"+f.suffix+".txt", "text/plain", []byte("PRIVATE_TAKEOVER_NEVER_SHARE VALUE=999"), 1)
	if err != nil {
		return err
	}
	taken, err := d.interactionControl(f.admin, run.ID, "takeover", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if taken.RequesterID != f.requester.id || taken.ControllerID != f.admin.id {
		return errors.New("run-controls: explicit shared takeover rewrote original requester")
	}
	if _, err = d.controlObserved(token, "running"); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.admin, map[string]any{"operation": "input", "message": "VALUE=1", "file_ids": []entityID{private.info.FileID}, "submission_key": uuid.NewString()}, http.StatusForbidden); err != nil {
		return err
	}
	if err = d.controlGate(token, "release", "", ""); err != nil {
		return err
	}
	// This model returns a tool result after its released computation. A later
	// provider call is necessary proof that explicitly taken shared work resumed.
	finished, err := d.publishedRun(f.admin, run.ID)
	if err != nil {
		return err
	}
	var shared struct {
		Sum int `json:"sum"`
	}
	if err = json.Unmarshal([]byte(finished.ResultText), &shared); err != nil {
		return err
	}
	if shared.Sum != 30 || strings.Contains(finished.ResultText, "PRIVATE_TAKEOVER_NEVER_SHARE") {
		return errors.New("run-controls: takeover lost shared input or inherited private material")
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/invite", f.admin.token, map[string]any{"user_ids": []entityID{f.newcomer.id}}, nil); err != nil {
		return err
	}
	historical, err := d.readRun(f.newcomer, run.ID)
	if err != nil {
		return err
	}
	if historical.ID != run.ID || historical.RequesterID != f.requester.id || historical.ControllerID != f.admin.id {
		return errors.New("run-controls: new member could not read existing execution history")
	}
	if _, err = d.conversationRuns(f.newcomer, f.conv); err != nil {
		return err
	}
	if finished.MessageID == nil {
		return errors.New("run-controls: shared takeover did not publish history")
	}
	message, err := d.identityMessage(f.newcomer, *finished.MessageID)
	if err != nil {
		return err
	}
	if message.Text.Text != finished.ResultText {
		return errors.New("run-controls: newly joined member could not read preexisting shared result")
	}
	if err = d.runtimeReject(http.MethodGet, "/messages/"+finished.MessageID.String(), f.owner, nil, http.StatusForbidden); err != nil {
		return err
	}
	for _, path := range []string{"/runs/" + run.ID.String(), "/conversations/" + f.conv.String() + "/runs"} {
		if err = d.runtimeReject(http.MethodGet, path, f.owner, nil, http.StatusForbidden); err != nil {
			return err
		}
	}
	return nil
}

func (f *controlFixture) botEligibility() error {
	d := f.d
	for _, reason := range []string{"remove", "disable", "permission"} {
		run, token, err := f.externalStart(reason, "pending")
		if err != nil {
			return err
		}
		request, err := f.x.request(run.ID)
		if err != nil {
			return err
		}
		binding, err := d.identityBinding(f.admin, f.conv, f.external.ID)
		if err != nil {
			return err
		}
		seed, err := d.sendMessage(f.admin, f.conv, "BOUND_HISTORY_SECRET_"+f.suffix, 0)
		if err != nil {
			return err
		}
		grant, err := d.identityGrant(f.admin, f.conv, f.external.ID, binding.BindingID, seed.Seq, seed.Seq)
		if err != nil {
			return err
		}
		if err = d.identityHistory(f.botToken, f.conv, binding.BindingID, grant, seed); err != nil {
			return err
		}
		path := "/convs/" + f.conv.String() + "/bots/" + f.external.ID.String()
		switch reason {
		case "remove":
			err = d.request(http.MethodDelete, path, f.admin.token, nil, nil)
		case "disable":
			err = d.request(http.MethodPut, "/bots/"+f.external.ID.String(), f.owner.token, map[string]string{"status": "disabled"}, nil)
		case "permission":
			err = d.request(http.MethodPut, path, f.admin.token, map[string]string{"bot_settings": `{"enabled":false}`}, nil)
		}
		if err != nil {
			return err
		}
		// Do not wait for a Kafka invalidation or run-monitor update before probing
		// an action boundary. These requests race the async notification by design.
		bot := account{token: f.botToken}
		historyDenial := http.StatusForbidden
		if reason == "disable" {
			historyDenial = http.StatusUnauthorized
		}
		if err = d.runtimeReject(http.MethodGet, identityHistoryPath(f.conv, binding.BindingID, grant, 0), bot, nil, historyDenial); err != nil {
			return err
		}
		output := executorPayload(request, "run.update", "completed", 2)
		output["msg_type"], output["content"], output["result_text"] = "text", map[string]any{"text": "FORBIDDEN_AFTER_" + reason}, "FORBIDDEN_AFTER_"+reason
		rest := *f.x
		rest.rest, rest.token = true, f.botToken
		if _, err = rest.update(output, true, false); err != nil {
			return err
		}
		stopping, err := d.controlRun(f.admin, run.ID, func(r publicRun) bool { return r.StopStatus == "requested" || r.StopStatus == "unknown" })
		if err != nil {
			return err
		}
		if stopping.Status == "stopped" || stopping.MessageID != nil {
			return errors.New("run-controls: revocation falsely reported actual stop or leaked output")
		}
		observed, err := d.controlObserve(token)
		if err != nil {
			return err
		}
		deadline := time.Now().Add(d.timeout)
		for len(observed.StopRequests) == 0 && time.Now().Before(deadline) {
			time.Sleep(30 * time.Millisecond)
			observed, err = d.controlObserve(token)
			if err != nil {
				return err
			}
		}
		if len(observed.StopRequests) == 0 {
			return errors.New("run-controls: revoked external work never received stop")
		}
		for _, stopRequest := range observed.StopRequests {
			if stopRequest.ID() != run.ID || stopRequest.Run.ID != "" || stopRequest.Operation != "stop" || stopRequest.BotID != f.external.ID || stopRequest.BindingID != binding.BindingID || stopRequest.Reason == "" {
				return errors.New("run-controls: security stop did not retain minimal routing/fencing or disclosed full Run")
			}
			for _, input := range stopRequest.Inputs {
				if input.Content != "" || len(input.Materials) != 0 {
					return errors.New("run-controls: security stop delivery leaked input/materials")
				}
			}
		}
		if reason == "disable" {
			before := observed.Ticks
			if err = d.controlGate(token, "reject", "", ""); err != nil {
				return err
			}
			if _, err = d.controlRun(f.admin, run.ID, func(r publicRun) bool { return r.StopStatus == "rejected" }); err != nil {
				return err
			}
			observed, err = d.controlObserve(token)
			if err != nil {
				return err
			}
			last := observed.Updates[len(observed.Updates)-1]
			if last.Response.Status != http.StatusOK || string(last.Payload["status"]) != `"stop_rejected"` || len(last.Payload["result_text"]) != 0 || len(last.Payload["content"]) != 0 {
				return errors.New("run-controls: disabled Bot could not report minimal stop refusal")
			}
			if observed.Ticks <= before {
				time.Sleep(90 * time.Millisecond)
			}
			continued, err := d.controlObserve(token)
			if err != nil {
				return err
			}
			if continued.Ticks <= before {
				return errors.New("run-controls: executor refusing stop did not actually continue external work")
			}
			if _, err = rest.update(output, true, false); err != nil {
				return err
			}
		}
		if err = d.controlGate(token, "confirm", "", ""); err != nil {
			return err
		}
		if err = f.confirmed(run, token); err != nil {
			return err
		}
		observed, err = d.controlObserve(token)
		if err != nil {
			return err
		}
		if len(observed.Updates) == 0 {
			return errors.New("run-controls: external cancellation did not produce an actual receipt")
		}
		last := observed.Updates[len(observed.Updates)-1]
		if last.Response.Status != http.StatusOK || string(last.Payload["status"]) != `"stopped"` || len(last.Payload["result_text"]) != 0 || len(last.Payload["content"]) != 0 {
			return fmt.Errorf("minimal stop confirmation after %s was not accepted without materials", reason)
		}
		// A valid same-key confirmation replay still works on the dedicated bot
		// credential path, but cannot authorize any content-bearing late result.
		payload := map[string]any{}
		for key, raw := range last.Payload {
			var value any
			if err = json.Unmarshal(raw, &value); err != nil {
				return err
			}
			payload[key] = value
		}
		if _, err = rest.update(payload, false, false); err != nil {
			return err
		}
		switch reason {
		case "remove":
			err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": f.external.ID}, nil)
		case "disable":
			err = d.request(http.MethodPut, "/bots/"+f.external.ID.String(), f.owner.token, map[string]string{"status": "active"}, nil)
		case "permission":
			err = d.request(http.MethodPut, path, f.admin.token, map[string]string{"bot_settings": `{"enabled":true}`}, nil)
		}
		if err != nil {
			return err
		}
		if err = d.runtimeReject(http.MethodPost, "/bots/"+f.external.ID.String()+"/runs", f.requester, map[string]any{"conv_id": f.conv, "parent_run_id": run.ID, "submission_key": uuid.NewString(), "message": "OLD_INTERACTION_MUST_NOT_REVIVE"}, http.StatusForbidden); err != nil {
			return err
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+run.ID.String()+"/control", f.requester, map[string]any{"operation": "input", "submission_key": uuid.NewString(), "message": "OLD_RUN_MUST_NOT_REVIVE"}, http.StatusForbidden); err != nil {
			return err
		}
		if _, err = rest.update(output, true, false); err != nil {
			return err
		}
		// Re-enabling may authorize fresh work, never the old run/interaction.
		fresh, freshToken, err := f.externalStart("fresh_"+reason, "confirm")
		if err != nil {
			return err
		}
		if fresh.ID == run.ID || fresh.InteractionID == run.InteractionID {
			return errors.New("run-controls: restored Bot revived old work")
		}
		if _, err = f.stop(f.requester, fresh, freshToken, uuid.NewString()); err != nil {
			return err
		}
		if err = f.confirmed(fresh, freshToken); err != nil {
			return err
		}
	}
	return nil
}

func (f *controlFixture) browserWork() error {
	// Leave real, observable running work for browser actions after automation.
	for _, mode := range []string{"pending", "reject", "confirm"} {
		run, token, err := f.externalStart("browser_"+mode, mode)
		if err != nil {
			return err
		}
		f.browser["runs"].(map[string]any)["browser_"+mode] = map[string]any{"id": run.ID, "token": token, "mode": mode}
	}
	return f.save()
}
