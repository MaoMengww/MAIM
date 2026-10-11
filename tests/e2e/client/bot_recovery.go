package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Recovery is observed at the authenticated Run/action/message contracts. The
// only fault controls belong to the external provider and isolated runner.
type recoveryFixture struct {
	authorizationFixture
}

type recoveryObservation struct {
	ModelRequests     int      `json:"model_requests"`
	ToolCalls         int      `json:"tool_calls"`
	Held              string   `json:"held"`
	ResponsesFinished bool     `json:"responses_finished"`
	Inputs            []string `json:"inputs"`
}

func (f *recoveryFixture) provider(token, operation, mode string) (recoveryObservation, error) {
	var observed recoveryObservation
	err := f.d.executorProvider("/recovery/"+operation, map[string]string{"token": token, "mode": mode}, &observed)
	return observed, err
}

func (f *recoveryFixture) held(token, phase string) error {
	deadline := time.Now().Add(f.d.timeout)
	for time.Now().Before(deadline) {
		observed, err := f.provider(token, "observe", "")
		if err != nil {
			return err
		}
		if observed.Held == phase {
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("recovery: external provider never held %s", phase)
}

func (f *recoveryFixture) checkpoint(name string, id entityID) error {
	if f.d.controlDir == "" {
		return errors.New("bot-recovery requires runner -control-dir")
	}
	path := filepath.Join(f.d.controlDir, "recovery-"+name)
	if err := os.WriteFile(path+".request", []byte("ready\n"), 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".done"); err == nil {
			if strings.HasSuffix(name, "-restart") || strings.HasSuffix(name, "-start") {
				// Container health is not readiness of bot-service's forwarding RPC.
				readyUntil := time.Now().Add(f.d.timeout)
				var stableSince time.Time
				for time.Now().Before(readyUntil) {
					_, err := f.d.readRun(f.requester, id)
					if err != nil {
						var unavailable *apiError
						if !errors.As(err, &unavailable) || unavailable.status != http.StatusServiceUnavailable {
							return err
						}
						stableSince = time.Time{}
					} else if stableSince.IsZero() {
						stableSince = time.Now()
					} else if time.Since(stableSince) >= 10*time.Second {
						return nil
					}
					time.Sleep(500 * time.Millisecond)
				}
				return errors.New("recovery: forwarded runtime did not become stably available")
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("recovery: runner did not acknowledge %s", name)
}

func (f *recoveryFixture) wait(id entityID, seconds time.Duration, predicate func(publicRun) bool) (publicRun, error) {
	deadline := time.Now().Add(seconds)
	var run publicRun
	for time.Now().Before(deadline) {
		var err error
		run, err = f.d.readRun(f.requester, id)
		if err != nil {
			var unavailable *apiError
			if !errors.As(err, &unavailable) || unavailable.status != http.StatusServiceUnavailable {
				return run, err
			}
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if predicate(run) {
			return run, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return run, fmt.Errorf("recovery: run=%s state=%s stage=%s executor=%s output=%s", id, run.Status, run.Stage, run.ExecutorStatus, run.OutputStatus)
}

func (f *recoveryFixture) counts(token string, calls int) (recoveryObservation, error) {
	observed, err := f.provider(token, "observe", "")
	if err == nil && observed.ToolCalls != calls {
		err = fmt.Errorf("recovery: %s external calls=%d expected=%d", token, observed.ToolCalls, calls)
	}
	return observed, err
}

func (f *recoveryFixture) uniqueMessage(run publicRun) error {
	if run.Status != "succeeded" || run.OutputStatus != "committed" || run.MessageID == nil {
		return errors.New("recovery: result not durably published")
	}
	message, err := f.d.identityMessage(f.requester, *run.MessageID)
	if err != nil {
		return err
	}
	if message.SenderType != "bot" || !hasEntityID(message.SenderID, run.BotID) || message.Text.Text != run.ResultText {
		return errors.New("recovery: persisted message identity/content differs from Run")
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err = f.d.request(http.MethodGet, "/convs/"+f.conv.String()+"/messages?limit=100", f.requester.token, nil, &history); err != nil {
		return err
	}
	count := 0
	for _, item := range history.Messages {
		if item.Text.Text == run.ResultText && hasEntityID(item.SenderID, run.BotID) {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("recovery: committed result has %d history messages, expected 1", count)
	}
	return nil
}

func (d *driver) botRecovery(address, secondAddress string) error {
	if d.controlDir == "" {
		return errors.New("bot-recovery requires run.py lifecycle controls")
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := recoveryFixture{authorizationFixture: authorizationFixture{d: d, suffix: suffix}}
	for _, item := range []struct {
		name    string
		account *account
	}{{"owner", &f.owner}, {"requester", &f.requester}, {"admin", &f.admin}} {
		*item.account, err = d.register("recovery_"+item.name, suffix)
		if err != nil {
			return err
		}
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{"type": "group", "group_name": "recovery-" + suffix, "member_ids": []entityID{f.owner.id, f.requester.id}}, &conv); err != nil {
		return err
	}
	f.conv = conv.ID
	f.model, err = d.fixtureModel(f.owner, "chat", "fixture-recovery-"+suffix)
	if err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "recovery-builtin-" + suffix, "type": "self_deployed", "model_id": f.model, "streaming_enabled": true}, &f.bot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "recovery-external-" + suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "capabilities": controlCapabilities}, &f.external); err != nil {
		return err
	}
	policies := map[string]any{}
	for _, risk := range []string{"read", "draft", "send"} {
		policies["fixture_auth_"+risk] = map[string]any{"risk": risk, "target_parameter": "target", "can_share": true, "conversation_ids": []entityID{f.conv}}
	}
	advanced, err := json.Marshal(map[string]any{"timeout": 180, "retry_count": 0, "tool_policies": policies})
	if err != nil {
		return err
	}
	var server struct {
		ID entityID `json:"id"`
	}
	if err = d.request(http.MethodPost, "/mcp-servers", f.owner.token, map[string]any{"name": "recovery-tools-" + suffix, "transport": "http-streamable", "url": d.provider + "/recovery/mcp", "enabled": true, "advanced_config": string(advanced)}, &server); err != nil {
		return err
	}
	f.server = server.ID
	for _, bot := range []entityID{f.bot.ID, f.external.ID} {
		if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": bot}, nil); err != nil {
			return err
		}
		if err = d.request(http.MethodPost, "/bots/"+bot.String()+"/mcp-servers", f.owner.token, map[string]any{"mcp_server_id": f.server}, nil); err != nil {
			return err
		}
	}
	accounts := map[string]any{}
	for _, item := range []struct {
		name    string
		account account
	}{{"owner", f.owner}, {"requester", f.requester}, {"admin", f.admin}} {
		accounts[item.name] = map[string]any{"id": item.account.id, "username": item.account.username, "password": item.account.password, "token": item.account.token}
	}
	f.browser = map[string]any{"accounts": accounts, "conversation_id": f.conv, "builtin_bot_id": f.bot.ID, "external_bot_id": f.external.ID, "server_id": f.server, "gateway": d.gateway, "realtime_a": address, "realtime_b": secondAddress, "provider": d.provider, "runs": map[string]any{}}
	if err = f.save(); err != nil {
		return err
	}
	// First tracer fails on the pre-issue15 implementation: no expiring claim,
	// no explicit lost state, and no durable re-claim fence.
	if err = f.externalLease(address, secondAddress); err != nil {
		return fmt.Errorf("recovery third-party: %w", err)
	}
	if err = f.waitingInput(); err != nil {
		return fmt.Errorf("recovery waiting input: %w", err)
	}
	if err = f.waitingAuthorization(); err != nil {
		return fmt.Errorf("recovery waiting authorization: %w", err)
	}
	if err = f.completedTool(); err != nil {
		return fmt.Errorf("recovery completed tool: %w", err)
	}
	if err = f.unknownWrite(); err != nil {
		return fmt.Errorf("recovery unknown write: %w", err)
	}
	if err = f.concurrentInput(); err != nil {
		return fmt.Errorf("recovery concurrent input: %w", err)
	}
	for _, risk := range []string{"read", "draft"} {
		if err = f.safeRetry(risk); err != nil {
			return fmt.Errorf("recovery uncertain %s: %w", risk, err)
		}
	}
	if err = f.pendingPublication(); err != nil {
		return fmt.Errorf("recovery pending publication: %w", err)
	}
	if err = f.externalUnknownWrite(secondAddress); err != nil {
		return fmt.Errorf("recovery external unknown write: %w", err)
	}
	if err = f.externalExpiredAuthorization(secondAddress); err != nil {
		return fmt.Errorf("recovery external expired authorization: %w", err)
	}
	if err = f.externalStoppedReceipt(secondAddress); err != nil {
		return fmt.Errorf("recovery external stopped receipt: %w", err)
	}
	if err = f.stopping(secondAddress); err != nil {
		return fmt.Errorf("recovery stopping: %w", err)
	}
	return nil
}

func (f *recoveryFixture) externalLease(address, secondAddress string) error {
	x, err := f.d.websocketExecutor(f.owner, f.external.ID, address, "recovery-lease-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	run, err := f.d.startRun(f.requester, f.external.ID, f.conv, "RECOVERY_EXTERNAL_"+f.suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	request, err := x.request(run.ID)
	if err != nil {
		return err
	}
	if err = f.record("external_claim", run, nil, "RECOVERY_EXTERNAL_"+f.suffix); err != nil {
		return err
	}
	claim := executorPayload(request, "run.claim", "accepted", 0)
	if _, err = x.update(claim, false, false); err != nil {
		return err
	}
	running := executorPayload(request, "run.update", "running", 1)
	if _, err = x.update(running, false, false); err != nil {
		return err
	}
	// A heartbeat has no business fields and does not consume business sequence.
	heartbeat := executorPayload(request, "run.heartbeat", "", 0)
	delete(heartbeat, "status")
	if _, err = x.update(heartbeat, false, false); err != nil {
		return err
	}
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	oldToken := "RECOVERY_OLD_" + f.suffix
	if _, err = f.provider(oldToken, "arm", "external"); err != nil {
		return err
	}
	oldAction := map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_read", "arguments": map[string]string{"target": "ledger", "text": oldToken}}
	var action authorizationAction
	if err = f.d.request(http.MethodPost, path, x.token, oldAction, &action); err != nil {
		return err
	}
	oldExecute := actionDecision(action, "execute", uuid.NewString())
	if err = f.d.request(http.MethodPost, path, x.token, oldExecute, nil); err != nil {
		return err
	}
	if _, err = f.counts(oldToken, 1); err != nil {
		return err
	}
	time.Sleep(35 * time.Second)
	heartbeat["update_key"] = uuid.NewString()
	if _, err = x.update(heartbeat, false, false); err != nil {
		return err
	}
	time.Sleep(30 * time.Second)
	current, err := f.d.readRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if current.ExecutorStatus == "lost" {
		return errors.New("heartbeat did not refresh the 60-second claim")
	}
	// Stop all executor traffic; expiry is observed publicly, not by rewriting DB time.
	if err = x.conn.Close(); err != nil {
		return err
	}
	lost, err := f.wait(run.ID, 50*time.Second, func(r publicRun) bool { return r.ExecutorStatus == "lost" && r.Stage == "executor_lost" })
	if err != nil {
		return err
	}
	if err = f.record("external_lost", lost, nil, "RECOVERY_EXTERNAL_"+f.suffix); err != nil {
		return err
	}
	x.rest = true
	if _, err = x.update(running, true, false); err != nil {
		return err
	}
	resumed, err := f.d.interactionControl(f.requester, run.ID, "resume", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if resumed.ID != run.ID || resumed.Generation <= request.Generation {
		return errors.New("lost Run was not safely re-assigned with a new generation")
	}
	y, err := f.d.websocketExecutor(f.owner, f.external.ID, secondAddress, "recovery-reclaim-"+f.suffix)
	if err != nil {
		return err
	}
	defer y.conn.Close()
	reassigned, err := y.requestVersion(run.ID, resumed.Generation, resumed.InputVersion, "")
	if err != nil {
		return err
	}
	if _, err = y.update(executorPayload(reassigned, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	// Replays cannot bypass the current generation even when their old key is known.
	for _, stale := range []map[string]any{claim, running, heartbeat} {
		if _, err = y.update(stale, true, false); err != nil {
			return err
		}
	}
	if err = f.d.authorizationReject(path, y.token, oldExecute); err != nil {
		return err
	}
	if _, err = f.counts(oldToken, 1); err != nil {
		return err
	}
	if err = f.d.authorizationReject(path, y.token, oldAction); err != nil {
		return err
	}
	result := executorPayload(reassigned, "run.update", "completed", 1)
	result["msg_type"], result["content"], result["result_text"] = "text", map[string]string{"text": "recovery-external-result:" + f.suffix}, "recovery-external-result:"+f.suffix
	if _, err = y.update(result, false, false); err != nil {
		return err
	}
	published, err := f.d.publishedRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if err = f.checkpoint("external-persistence-restart", run.ID); err != nil {
		return err
	}
	if published.ExecutorLeaseUntil == nil {
		return errors.New("completed execution has no durable lease deadline")
	}
	// Replay is an acknowledgement of a terminal fact, even after its former
	// execution lease expires. It must not revive or renew that execution.
	if remaining := time.Until(time.Unix(*published.ExecutorLeaseUntil+1, 0)); remaining > 0 {
		time.Sleep(remaining)
	}
	y.rest = true
	for range 2 {
		if _, err = y.update(result, false, false); err != nil {
			return err
		}
	}
	replayed, err := f.wait(run.ID, f.d.timeout, func(publicRun) bool { return true })
	if err != nil {
		return err
	}
	if replayed.Version != published.Version || replayed.Status != published.Status || !hasEntityID(replayed.MessageID, *published.MessageID) || replayed.ExecutorLeaseUntil == nil || *replayed.ExecutorLeaseUntil != *published.ExecutorLeaseUntil {
		return errors.New("terminal replay changed the durable outcome or renewed execution")
	}
	changed := maps.Clone(result)
	changed["result_text"] = "different-content"
	changed["content"] = map[string]string{"text": "different-content"}
	if _, err = y.update(changed, true, false); err != nil {
		return err
	}
	if err = f.uniqueMessage(published); err != nil {
		return err
	}
	if err = f.record("external_result", published, nil, "RECOVERY_EXTERNAL_"+f.suffix); err != nil {
		return err
	}
	// Terminal retry is new linked work, never revival of the old claim.
	retry, err := f.d.interactionStart(f.requester, f.external.ID, f.conv, "RECOVERY_RETRY_"+f.suffix, map[string]any{"parent_run_id": run.ID})
	if err != nil {
		return err
	}
	if retry.ID == run.ID || !hasEntityID(retry.ParentRunID, run.ID) {
		return errors.New("terminal retry did not create a new parent-linked Run")
	}
	if err = f.record("terminal_retry", retry.publicRun, nil, "RECOVERY_RETRY_"+f.suffix); err != nil {
		return err
	}
	request2, err := y.request(retry.ID)
	if err != nil {
		return err
	}
	if _, err = y.complete(request2, "text", map[string]any{"text": "retry-result:" + f.suffix}, "retry-result:"+f.suffix); err != nil {
		return err
	}
	if _, err = f.d.publishedRun(f.requester, retry.ID); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: external lost=%s generation=%d->%d sequence_reset=0 replay_fenced=true result_messages=1 retry_new_run=true\n", lost.ExecutorStatus, request.Generation, reassigned.Generation)
	return nil
}

func (f *recoveryFixture) waitingInput() error {
	token := "RECOVERY_INPUT_" + f.suffix
	if _, err := f.provider(token, "arm", "waiting-input"); err != nil {
		return err
	}
	key := uuid.NewString()
	run, err := f.d.startRun(f.requester, f.bot.ID, f.conv, token, key, nil)
	if err != nil {
		return err
	}
	waiting, err := f.d.interactionWait(f.requester, run.ID, "waiting_input")
	if err != nil {
		return err
	}
	if err = f.record("waiting_input", waiting.publicRun, nil, token); err != nil {
		return err
	}
	before, err := f.counts(token, 1)
	if err != nil {
		return err
	}
	if err = f.checkpoint("waiting-input-restart", run.ID); err != nil {
		return err
	}
	replay, err := f.d.startRun(f.requester, f.bot.ID, f.conv, token, key, nil)
	if err != nil {
		return err
	}
	if replay.ID != run.ID {
		return errors.New("persisted submission replay created another Run")
	}
	if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.requester.token, map[string]any{"conv_id": f.conv, "submission_key": key, "message": token + "changed"}); err != nil {
		return err
	}
	after, err := f.counts(token, 1)
	if err != nil {
		return err
	}
	if after.ModelRequests != before.ModelRequests {
		return errors.New("waiting input restart repeated a completed model result")
	}
	current, err := f.d.readRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if current.Status != "waiting_input" || current.InputVersion != waiting.InputVersion {
		return errors.New("input wait did not survive restart")
	}
	inputKey := uuid.NewString()
	accepted, err := f.d.interactionControl(f.requester, run.ID, "input", "second-input", inputKey, nil)
	if err != nil {
		return err
	}
	if err = f.d.authorizationReject("/runs/"+run.ID.String()+"/control", f.requester.token, map[string]any{"operation": "input", "submission_key": inputKey, "message": "different-input"}); err != nil {
		return err
	}
	finished, err := f.d.interactionWait(f.requester, run.ID, "succeeded")
	if err != nil {
		return err
	}
	if err = f.checkpoint("input-persistence-restart", run.ID); err != nil {
		return err
	}
	for range 2 {
		repeated, err := f.d.interactionControl(f.requester, run.ID, "input", "second-input", inputKey, nil)
		if err != nil {
			return err
		}
		if repeated.InputVersion != accepted.InputVersion {
			return errors.New("duplicate input advanced accepted version")
		}
	}
	observed, err := f.counts(token, 1)
	if err != nil {
		return err
	}
	if len(observed.Inputs) != 2 || observed.Inputs[0] != token || observed.Inputs[1] != "second-input" {
		return errors.New("durable input replay changed the actual accepted model prefix")
	}
	if err = f.uniqueMessage(finished.publicRun); err != nil {
		return err
	}
	if err = f.record("input_result", finished.publicRun, nil, token); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: waiting_input models=%d preserved=true tools=1 input_version=%d duplicate_input=true state=%s\n", before.ModelRequests, accepted.InputVersion, finished.Status)
	return nil
}

func (f *recoveryFixture) pendingAction(token, mode string) (publicRun, authorizationAction, error) {
	if _, err := f.provider(token, "arm", mode); err != nil {
		return publicRun{}, authorizationAction{}, err
	}
	run, err := f.start(token, nil)
	if err != nil {
		return run, authorizationAction{}, err
	}
	deadline := time.Now().Add(f.d.timeout)
	for time.Now().Before(deadline) {
		var actions []authorizationAction
		if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
			return run, authorizationAction{}, err
		}
		if len(actions) == 1 && actions[0].Status == "pending" {
			if err = f.record(mode+"_pending", run, &actions[0], token); err != nil {
				return run, actions[0], err
			}
			return run, actions[0], nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return run, authorizationAction{}, errors.New("recovery: exact write action never waited for authorization")
}

func (f *recoveryFixture) waitingAuthorization() error {
	token := "RECOVERY_AUTH_" + f.suffix
	run, action, err := f.pendingAction(token, "write")
	if err != nil {
		return err
	}
	before, err := f.counts(token, 0)
	if err != nil {
		return err
	}
	if err = f.checkpoint("waiting-auth-restart", run.ID); err != nil {
		return err
	}
	current, err := f.d.readRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if current.Status != "waiting_authorization" {
		return errors.New("authorization wait lost at restart")
	}
	if _, err = f.counts(token, 0); err != nil {
		return err
	}
	after, err := f.counts(token, 0)
	if err != nil {
		return err
	}
	if after.ModelRequests != before.ModelRequests {
		return errors.New("authorization wait restart repeated the completed model request")
	}
	decision := actionDecision(action, "approve", uuid.NewString())
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, decision, nil); err != nil {
		return err
	}
	finished, err := f.d.interactionWait(f.requester, run.ID, "succeeded")
	if err != nil {
		return err
	}
	for range 2 {
		if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, decision, nil); err != nil {
			return err
		}
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	if err = f.uniqueMessage(finished.publicRun); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: waiting_authorization models_before=%d write_count=1 repeated_approval=true state=%s\n", before.ModelRequests, finished.Status)
	return nil
}

func (f *recoveryFixture) completedTool() error {
	token := "RECOVERY_COMPLETED_" + f.suffix
	run, action, err := f.pendingAction(token, "completed-tool")
	if err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	if err = f.held(token, "model-after-tool"); err != nil {
		return err
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	if err = f.checkpoint("completed-tool-kill", run.ID); err != nil {
		return err
	}
	if _, err = f.provider(token, "release", ""); err != nil {
		return err
	}
	if err = f.checkpoint("completed-tool-start", run.ID); err != nil {
		return err
	}
	finished, err := f.d.interactionWait(f.requester, run.ID, "succeeded")
	if err != nil {
		return err
	}
	observed, err := f.counts(token, 1)
	if err != nil {
		return err
	}
	if err = f.uniqueMessage(finished.publicRun); err != nil {
		return err
	}
	if err = f.record("completed_write", finished.publicRun, nil, token); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: completed_tool SIGKILL tool_count=%d model_calls=%d state=%s\n", observed.ToolCalls, observed.ModelRequests, finished.Status)
	return nil
}

func (f *recoveryFixture) unknownWrite() error {
	token := "RECOVERY_UNKNOWN_" + f.suffix
	run, action, err := f.pendingAction(token, "unknown-write")
	if err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	if err = f.held(token, "tool-after-effect"); err != nil {
		return err
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	if err = f.checkpoint("unknown-write-kill", run.ID); err != nil {
		return err
	}
	if _, err = f.provider(token, "release", ""); err != nil {
		return err
	}
	if err = f.checkpoint("unknown-write-start", run.ID); err != nil {
		return err
	}
	unknown, err := f.wait(run.ID, f.d.timeout, func(r publicRun) bool {
		return r.Status == "waiting_authorization" && r.Stage == "action_outcome_unknown"
	})
	if err != nil {
		return err
	}
	var actions []authorizationAction
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
		return err
	}
	if len(actions) != 1 || actions[0].Status != "unknown" {
		return errors.New("uncertain write is not publicly unknown")
	}
	if err = f.record("unknown_write", unknown, &actions[0], token); err != nil {
		return err
	}
	if err = f.d.authorizationReject("/runs/"+run.ID.String()+"/control", f.requester.token, map[string]any{"operation": "resume", "submission_key": uuid.NewString()}); err != nil {
		return err
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: completed_external_write_unreported SIGKILL action=%s count=1 state=%s stage=%s automatic_resume=denied\n", actions[0].Status, unknown.Status, unknown.Stage)
	return nil
}

func (f *recoveryFixture) safeRetry(risk string) error {
	token := "RECOVERY_SAFE_" + risk + "_" + f.suffix
	if _, err := f.provider(token, "arm", "uncertain-"+risk); err != nil {
		return err
	}
	run, err := f.start(token, nil)
	if err != nil {
		return err
	}
	if err = f.held(token, "tool-after-effect"); err != nil {
		return err
	}
	if err = f.checkpoint("safe-"+risk+"-kill", run.ID); err != nil {
		return err
	}
	if _, err = f.provider(token, "release", ""); err != nil {
		return err
	}
	if err = f.checkpoint("safe-"+risk+"-start", run.ID); err != nil {
		return err
	}
	finished, err := f.d.interactionWait(f.requester, run.ID, "succeeded")
	if err != nil {
		return err
	}
	if _, err = f.counts(token, 2); err != nil {
		return err
	}
	if err = f.record("safe_"+risk, finished.publicRun, nil, token); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: uncertain_%s safe_retry=true external_calls=2 state=%s\n", risk, finished.Status)
	return nil
}

func (f *recoveryFixture) pendingPublication() error {
	token := "RECOVERY_PUBLICATION_" + f.suffix
	if _, err := f.provider(token, "arm", "publication"); err != nil {
		return err
	}
	run, err := f.start(token, nil)
	if err != nil {
		return err
	}
	if err = f.held(token, "model-after-tool"); err != nil {
		return err
	}
	if err = f.checkpoint("publication-message-stop", run.ID); err != nil {
		return err
	}
	if _, err = f.provider(token, "release", ""); err != nil {
		return err
	}
	deadline := time.Now().Add(f.d.timeout)
	for {
		observed, err := f.provider(token, "observe", "")
		if err != nil {
			return err
		}
		if observed.ResponsesFinished {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("recovery final model response was not delivered")
		}
		time.Sleep(30 * time.Millisecond)
	}
	// GET Run itself requires message-service membership RPC. Keep the real
	// submission outage open after the complete response, then kill the worker;
	// restored REST output and unchanged provider counts prove durable replay.
	time.Sleep(5 * time.Second)
	before, err := f.counts(token, 1)
	if err != nil {
		return err
	}
	if err = f.checkpoint("publication-runtime-kill", run.ID); err != nil {
		return err
	}
	if err = f.checkpoint("publication-services-start", run.ID); err != nil {
		return err
	}
	published, err := f.d.publishedRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	after, err := f.counts(token, 1)
	if err != nil {
		return err
	}
	if before.ModelRequests != after.ModelRequests || published.ResultText != "recovery-result:"+token {
		return errors.New("pending output replay recomputed the completed model result")
	}
	if err = f.uniqueMessage(published); err != nil {
		return err
	}
	if err = f.record("publication_result", published, nil, token); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: message_service_down models=%d->%d tools=1 message_count=1 state=%s output=%s\n", before.ModelRequests, after.ModelRequests, published.Status, published.OutputStatus)
	return nil
}

func (f *recoveryFixture) stopping(address string) error {
	x, err := f.d.websocketExecutor(f.owner, f.external.ID, address, "recovery-stopping-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	run, err := f.d.startRun(f.requester, f.external.ID, f.conv, "RECOVERY_STOP_"+f.suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	request, err := x.request(run.ID)
	if err != nil {
		return err
	}
	for _, payload := range []map[string]any{executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
		if _, err = x.update(payload, false, false); err != nil {
			return err
		}
	}
	stopping, err := f.d.interactionControl(f.requester, run.ID, "stop", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if err = f.record("stopping_unconfirmed", stopping.publicRun, nil, "RECOVERY_STOP_"+f.suffix); err != nil {
		return err
	}
	if err = f.checkpoint("stopping-restart", run.ID); err != nil {
		return err
	}
	if stopping.ExecutorLeaseUntil == nil {
		return errors.New("claimed stopping Run has no lease deadline")
	}
	if remaining := time.Until(time.Unix(*stopping.ExecutorLeaseUntil+1, 0)); remaining > 0 {
		time.Sleep(remaining)
	}
	current, err := f.d.readRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if current.Status != "stopping" || current.StopStatus == "confirmed" {
		return fmt.Errorf("unconfirmed stop falsely restored as %s/%s", current.Status, current.StopStatus)
	}
	stopRequest := request
	stopRequest.Generation, stopRequest.InputVersion, stopRequest.Operation = stopping.Generation, stopping.InputVersion, "stop"
	confirmation := executorPayload(stopRequest, "run.update", "stopped", 2)
	if _, err = x.update(confirmation, false, false); err != nil {
		return err
	}
	stopped, err := f.d.interactionWait(f.requester, run.ID, "stopped")
	if err != nil {
		return err
	}
	if stopped.MessageID != nil || stopped.StopStatus != "confirmed" || stopped.ExecutorLeaseUntil == nil || *stopped.ExecutorLeaseUntil != *stopping.ExecutorLeaseUntil {
		return errors.New("stop confirmation did not fence output")
	}
	fmt.Printf("Recovery evidence: stopping_restart unconfirmed_preserved=true final=%s stop=%s messages=0\n", stopped.Status, stopped.StopStatus)
	return nil
}

func (f *recoveryFixture) externalAction(token, mode, address string, lifetime time.Duration) (*controlledExecutor, publicRun, authorizationAction, error) {
	if _, err := f.provider(token, "arm", mode); err != nil {
		return nil, publicRun{}, authorizationAction{}, err
	}
	x, err := f.d.websocketExecutor(f.owner, f.external.ID, address, "recovery-action-"+uuid.NewString())
	if err != nil {
		return nil, publicRun{}, authorizationAction{}, err
	}
	run, err := f.d.startRun(f.requester, f.external.ID, f.conv, token, uuid.NewString(), nil)
	if err != nil {
		x.conn.Close()
		return nil, run, authorizationAction{}, err
	}
	request, err := x.request(run.ID)
	if err != nil {
		x.conn.Close()
		return nil, run, authorizationAction{}, err
	}
	for _, payload := range []map[string]any{executorPayload(request, "run.claim", "accepted", 0), executorPayload(request, "run.update", "running", 1)} {
		if _, err = x.update(payload, false, false); err != nil {
			x.conn.Close()
			return nil, run, authorizationAction{}, err
		}
	}
	body := map[string]any{"op": "request", "run_id": run.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": map[string]string{"target": "controlled-external-recovery-ledger", "text": token}}
	if lifetime > 0 {
		body["expires_at"] = time.Now().Add(lifetime).Unix()
	}
	var action authorizationAction
	if err = f.d.request(http.MethodPost, "/bots/"+f.external.ID.String()+"/executor/actions", x.token, body, &action); err != nil {
		x.conn.Close()
		return nil, run, action, err
	}
	if action.Status != "pending" || action.RunID != run.ID || action.Risk != "send" {
		x.conn.Close()
		return nil, run, action, errors.New("external recovery write did not require exact owner authorization")
	}
	if err = f.record(mode+"_external_pending", run, &action, token); err != nil {
		x.conn.Close()
		return nil, run, action, err
	}
	return x, run, action, nil
}

func (f *recoveryFixture) externalUnknownWrite(address string) error {
	token := "RECOVERY_EXTERNAL_UNKNOWN_" + f.suffix
	x, run, action, err := f.externalAction(token, "unknown-write", address, 0)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	execute := actionDecision(action, "execute", uuid.NewString())
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	// The request really crosses the gateway/runtime/MCP boundary. A long client
	// deadline keeps cancellation from manufacturing uncertainty before SIGKILL.
	execution := *f.d
	execution.timeout = 5 * time.Minute
	execution.client = newHTTPClient(execution.timeout)
	defer execution.client.CloseIdleConnections()
	result := make(chan error, 1)
	go func() { result <- execution.request(http.MethodPost, path, x.token, execute, nil) }()
	if err = f.held(token, "tool-after-effect"); err != nil {
		return err
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	if err = f.checkpoint("external-unknown-kill", run.ID); err != nil {
		return err
	}
	if _, err = f.provider(token, "release", ""); err != nil {
		return err
	}
	select {
	case requestErr := <-result:
		if requestErr == nil {
			return errors.New("killed external action falsely reported a completed result")
		}
	case <-time.After(f.d.timeout):
		return errors.New("killed external action HTTP request did not settle")
	}
	if err = f.checkpoint("external-unknown-start", run.ID); err != nil {
		return err
	}
	// Third-party claim expiry remains the real 60-second public contract.
	unknown, err := f.wait(run.ID, 80*time.Second, func(r publicRun) bool {
		return r.Status == "waiting_authorization" && r.Stage == "action_outcome_unknown"
	})
	if err != nil {
		return err
	}
	var actions []authorizationAction
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
		return err
	}
	if len(actions) != 1 || actions[0].ID != action.ID || actions[0].Status != "unknown" || unknown.MessageID != nil {
		return errors.New("external uncertain write did not restore public unknown/manual waiting")
	}
	if err = f.record("external_unknown_write", unknown, &actions[0], token); err != nil {
		return err
	}
	if err = f.d.authorizationReject("/runs/"+run.ID.String()+"/control", f.requester.token, map[string]any{"operation": "resume", "submission_key": uuid.NewString()}); err != nil {
		return err
	}
	// The old execution's exact key and generation cannot bypass the uncertainty
	// fence; neither may a new key silently repeat the external send.
	if err = f.d.authorizationReject(path, x.token, execute); err != nil {
		return err
	}
	newKey := maps.Clone(execute)
	newKey["submission_key"] = uuid.NewString()
	if err = f.d.authorizationReject(path, x.token, newKey); err != nil {
		return err
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: external_completed_write_unreported SIGKILL action=%s count=1 state=%s stage=%s resume=denied old_execution=denied messages=0\n", actions[0].Status, unknown.Status, unknown.Stage)
	return nil
}

func (f *recoveryFixture) externalExpiredAuthorization(address string) error {
	token := "RECOVERY_EXTERNAL_EXPIRED_" + f.suffix
	x, run, action, err := f.externalAction(token, "expired-write", address, 3*time.Second)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	if _, err = f.counts(token, 0); err != nil {
		return err
	}
	// Let a valid publicly accepted pending approval actually age out. Neither
	// malformed expiry values nor database timestamp edits stand in for expiry.
	time.Sleep(max(0, time.Until(time.Unix(action.ExpiresAt, 0))) + 150*time.Millisecond)
	if err = f.checkpoint("external-expired-auth-restart", run.ID); err != nil {
		return err
	}
	current, err := f.d.readRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	var actions []authorizationAction
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
		return err
	}
	if len(actions) != 1 || actions[0].ID != action.ID || actions[0].Status != "expired" || actions[0].CanApprove {
		return errors.New("expired pending approval became usable after process restart")
	}
	if err = f.d.authorizationReject("/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString())); err != nil {
		return err
	}
	var outcome struct {
		Action authorizationAction `json:"action"`
		Result string              `json:"result"`
	}
	err = f.d.request(http.MethodPost, "/bots/"+f.external.ID.String()+"/executor/actions", x.token, actionDecision(action, "execute", uuid.NewString()), &outcome)
	if err != nil {
		var denied *apiError
		if !errors.As(err, &denied) || denied.status < 400 || denied.status >= 500 {
			return err
		}
	} else {
		var blocked struct {
			Status string `json:"status"`
		}
		if json.Unmarshal([]byte(outcome.Result), &blocked) != nil || blocked.Status != "expired" || outcome.Action.ID != action.ID || outcome.Action.Status != "expired" {
			return errors.New("expired execution did not return an authoritative blocked outcome")
		}
	}
	if _, err = f.counts(token, 0); err != nil {
		return err
	}
	if current.MessageID != nil {
		return errors.New("expired unexecuted write published a fabricated output")
	}
	if err = f.record("external_expired_authorization", current, &actions[0], token); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: external_pending_approval_restart action=%s expires_at=%d count=0 approve=denied execute=blocked state=%s messages=0\n", actions[0].Status, action.ExpiresAt, current.Status)
	return nil
}

func (f *recoveryFixture) concurrentInput() error {
	token := "RECOVERY_CONCURRENT_INPUT_" + f.suffix
	supplement := "recovery-live-supplement-" + f.suffix
	if _, err := f.provider(token, "arm", "uncertain-read"); err != nil {
		return err
	}
	run, err := f.start(token, nil)
	if err != nil {
		return err
	}
	if err = f.held(token, "tool-after-effect"); err != nil {
		return err
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	live, err := f.d.readRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	if err = f.record("concurrent_input_held", live, nil, token); err != nil {
		return err
	}
	accepted, err := f.d.interactionControl(f.requester, run.ID, "input", supplement, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if accepted.ID != run.ID || accepted.Generation != live.Generation || accepted.InputVersion != live.InputVersion+1 {
		return errors.New("live supplement replaced the executing generation or lost its accepted input version")
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	if _, err = f.provider(token, "release", ""); err != nil {
		return err
	}
	finished, err := f.d.interactionWait(f.requester, run.ID, "succeeded")
	if err != nil {
		return err
	}
	observed, err := f.counts(token, 1)
	if err != nil {
		return err
	}
	if finished.ID != run.ID || finished.InputVersion != accepted.InputVersion || len(observed.Inputs) != 2 || observed.Inputs[0] != token || observed.Inputs[1] != supplement {
		return errors.New("concurrent input did not reach the same Run's real resumed model prefix")
	}
	if err = f.uniqueMessage(finished.publicRun); err != nil {
		return err
	}
	if err = f.record("concurrent_input_result", finished.publicRun, nil, token); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: live_read_concurrent_input generation_preserved=true input_version=%d->%d external_calls=1 actual_model_inputs=2 state=%s\n", live.InputVersion, finished.InputVersion, finished.Status)
	return nil
}

func (f *recoveryFixture) externalStoppedReceipt(address string) error {
	token := "RECOVERY_EXTERNAL_STOPPED_RECEIPT_" + f.suffix
	x, run, action, err := f.externalAction(token, "unknown-write", address, 0)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	execution := *f.d
	execution.timeout = 5 * time.Minute
	execution.client = newHTTPClient(execution.timeout)
	defer execution.client.CloseIdleConnections()
	result := make(chan error, 1)
	go func() {
		result <- execution.request(http.MethodPost, "/bots/"+f.external.ID.String()+"/executor/actions", x.token, actionDecision(action, "execute", uuid.NewString()), nil)
	}()
	if err = f.held(token, "tool-after-effect"); err != nil {
		return err
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	stopping, err := f.d.interactionControl(f.requester, run.ID, "stop", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	stopRequest := executorRequest{RunID: run.ID, Generation: stopping.Generation, InputVersion: stopping.InputVersion}
	if _, err = x.update(executorPayload(stopRequest, "run.update", "stopped", 2), false, false); err != nil {
		return err
	}
	stopped, err := f.d.interactionWait(f.requester, run.ID, "stopped")
	if err != nil {
		return err
	}
	if stopped.StopStatus != "confirmed" || stopped.MessageID != nil {
		return errors.New("external action stop did not terminally fence Run output")
	}
	if err = f.record("terminal_action_stopped", stopped.publicRun, &action, token); err != nil {
		return err
	}
	if _, err = f.provider(token, "release", ""); err != nil {
		return err
	}
	select {
	case err = <-result:
		if err != nil {
			return fmt.Errorf("terminal external action receipt was rejected: %w", err)
		}
	case <-time.After(f.d.timeout):
		return errors.New("terminal external action receipt never settled")
	}
	current, err := f.d.readRun(f.requester, run.ID)
	if err != nil {
		return err
	}
	var actions []authorizationAction
	if err = f.d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, map[string]any{"op": "list", "run_id": run.ID}, &actions); err != nil {
		return err
	}
	if len(actions) != 1 || actions[0].ID != action.ID || actions[0].Status != "succeeded" {
		return errors.New("late terminal action fact remained executing instead of succeeded")
	}
	if current.Status != "stopped" || current.Stage != stopped.Stage || current.StopStatus != "confirmed" || current.MessageID != nil {
		return errors.New("late external action fact reopened or rewrote terminal Run state")
	}
	if _, err = f.counts(token, 1); err != nil {
		return err
	}
	if err = f.record("terminal_action_receipt", current, &actions[0], token); err != nil {
		return err
	}
	fmt.Printf("Recovery evidence: external_write_stop_before_receipt action=%s external_count=1 run=%s stage=%s stop=%s messages=0\n", actions[0].Status, current.Status, current.Stage, current.StopStatus)
	return nil
}
