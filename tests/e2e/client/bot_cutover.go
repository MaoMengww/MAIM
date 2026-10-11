package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// This is one fresh composite public journey, not a replay of earlier scenarios.
// The external fixtures observe model/tool HTTP; all business state is REST/WS.
type cutoverFixture struct {
	authorizationFixture
	address, secondAddress string
	a, b                   *p6Socket
	runs                   map[string]map[string]any
}

func (f *cutoverFixture) saveCutover(label string, run publicRun) error {
	if run.ID != "" {
		f.runs[label] = map[string]any{"id": run.ID, "bot_id": run.BotID, "conv_id": run.ConvID,
			"version": run.Version, "input_version": run.InputVersion, "generation": run.Generation,
			"status": run.Status, "stop_status": run.StopStatus, "output_status": run.OutputStatus}
	}
	accounts := map[string]any{}
	for name, user := range map[string]account{"owner": f.owner, "requester": f.requester, "admin": f.admin, "member": f.member} {
		accounts[name] = map[string]any{"id": user.id, "username": user.username, "password": user.password, "token": user.token}
	}
	raw, err := json.MarshalIndent(map[string]any{
		"accounts": accounts, "conversation_id": f.conv, "second_conversation_id": f.otherConv,
		"builtin_bot_id": f.bot.ID, "external_bot_id": f.external.ID, "server_id": f.server,
		"gateway": f.d.gateway, "realtime_a": f.address, "realtime_b": f.secondAddress,
		"runs": f.runs,
	}, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(f.d.controlDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.d.controlDir, "cutover-browser.json"), append(raw, '\n'), 0600)
}

func (f *cutoverFixture) evidence(label string, run publicRun) error {
	if err := f.saveCutover(label, run); err != nil {
		return err
	}
	// Deliberately omit credentials, model inputs, private results and tool bodies.
	fmt.Printf("CUTOVER public: phase=%s run=%s bot=%s conv=%s version=%d input_version=%d generation=%d status=%s executor=%s stop=%s output=%s\n",
		label, run.ID, run.BotID, run.ConvID, run.Version, run.InputVersion, run.Generation,
		run.Status, run.ExecutorStatus, run.StopStatus, run.OutputStatus)
	return nil
}

func (d *driver) botCutover(address, secondAddress string) error {
	if d.controlDir == "" || address == "" || secondAddress == "" || address == secondAddress {
		return errors.New("bot-cutover requires private -control-dir and two distinct realtime addresses")
	}
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := cutoverFixture{authorizationFixture: authorizationFixture{d: d, suffix: suffix}, address: address, secondAddress: secondAddress, runs: map[string]map[string]any{}}
	for name, user := range map[string]*account{"owner": &f.owner, "requester": &f.requester, "admin": &f.admin, "member": &f.member} {
		*user, err = d.register("cutover_"+name, suffix)
		if err != nil {
			return err
		}
	}
	for i, id := range []*entityID{&f.conv, &f.otherConv} {
		var conv struct {
			ID entityID `json:"conversation_id"`
		}
		if err = d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{"type": "group", "group_name": fmt.Sprintf("cutover-%d-%s", i, suffix), "member_ids": []entityID{f.requester.id, f.member.id, f.owner.id}}, &conv); err != nil {
			return err
		}
		*id = conv.ID
	}
	f.model, err = d.fixtureModel(f.owner, "chat", "fixture-interaction-cutover-"+suffix)
	if err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "cutover-builtin-" + suffix, "type": "self_deployed", "model_id": f.model, "streaming_enabled": false, "enable_knowledge": false}, &f.bot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "cutover-external-" + suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "capabilities": controlCapabilities}, &f.external); err != nil {
		return err
	}
	for _, conv := range []entityID{f.conv, f.otherConv} {
		for _, bot := range []entityID{f.bot.ID, f.external.ID} {
			if err = d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": bot}, nil); err != nil {
				return err
			}
		}
	}
	policies := map[string]any{"fixture_auth_send": map[string]any{"risk": "send", "target_parameter": "target", "can_share": true, "conversation_ids": []entityID{f.conv}}}
	advanced, err := json.Marshal(map[string]any{"tool_policies": policies})
	if err != nil {
		return err
	}
	var server struct {
		ID entityID `json:"id"`
	}
	if err = d.request(http.MethodPost, "/mcp-servers", f.owner.token, map[string]any{"name": "cutover-actions-" + suffix, "transport": "http-streamable", "url": d.provider + "/authorization/mcp", "enabled": true, "advanced_config": string(advanced)}, &server); err != nil {
		return err
	}
	f.server = server.ID
	if err = d.request(http.MethodPost, "/bots/"+f.external.ID.String()+"/mcp-servers", f.owner.token, map[string]any{"mcp_server_id": f.server}, nil); err != nil {
		return err
	}
	f.a, err = d.p6Connect(address, f.requester)
	if err != nil {
		return err
	}
	defer func() { f.a.close() }()
	f.b, err = d.p6Connect(secondAddress, f.admin)
	if err != nil {
		return err
	}
	defer func() { f.b.close() }()
	if err = f.saveCutover("setup", publicRun{}); err != nil {
		return err
	}
	if err = f.builtinComposite(); err != nil {
		return fmt.Errorf("cutover builtin composite: %w", err)
	}
	if err = f.ruleRecoveryComposite(); err != nil {
		return fmt.Errorf("cutover rule/action/recovery composite: %w", err)
	}
	if err = f.stopAndRevokeComposite(); err != nil {
		return fmt.Errorf("cutover linked stop/revocation composite: %w", err)
	}
	fmt.Println("E2E PASS: bot-cutover fresh composite REST/WS/provider journey; private browser fixture saved; no real paid/mail/delete tool invoked; provider cache hit not claimed")
	return nil
}

func (f *cutoverFixture) builtinComposite() (result error) {
	d := f.d
	unrelated := "CUTOVER_UNRELATED_" + f.suffix
	if _, err := d.sendMessage(f.admin, f.conv, unrelated+" VALUE=999", 0); err != nil {
		return err
	}
	source, err := d.sendMessage(f.requester, f.conv, "CUTOVER_ACCEPTED_SOURCE_"+f.suffix+" VALUE=3", 0)
	if err != nil {
		return err
	}
	for _, socket := range []*p6Socket{f.a, f.b} {
		if _, err = d.p6Message(socket, source); err != nil {
			return err
		}
	}
	token := "INTERACTION_WAIT_CUTOVER_" + f.suffix
	waiting, err := d.interactionStart(f.requester, f.bot.ID, f.conv, token+" VALUE=5", map[string]any{"message_ids": []entityID{source.MessageID}})
	if err != nil {
		return err
	}
	waiting, err = d.interactionWait(f.admin, waiting.ID, "waiting_input")
	if err != nil {
		return err
	}
	for _, socket := range []*p6Socket{f.a, f.b} {
		if _, err = d.p6Wait(socket, 0, "cutover-wait", func(e p6Event) bool {
			return e.Type == "bot.run.updated" && e.Run.ID == waiting.ID && e.Run.Status == "waiting_input"
		}); err != nil {
			return err
		}
	}
	if err = f.evidence("builtin_waiting", waiting.publicRun); err != nil {
		return err
	}
	// Other topics and the other binding run while this same Bot is paused.
	parallel, err := d.interactionStart(f.admin, f.bot.ID, f.conv, "INTERACTION_CUTOVER_PARALLEL_"+f.suffix+" VALUE=17", nil)
	if err != nil {
		return err
	}
	second, err := d.interactionStart(f.requester, f.bot.ID, f.otherConv, "INTERACTION_CUTOVER_SECOND_"+f.suffix+" VALUE=23", nil)
	if err != nil {
		return err
	}
	if parallel.InteractionID == waiting.InteractionID || second.InteractionID == waiting.InteractionID || second.BindingID == waiting.BindingID || second.BotID != waiting.BotID {
		return errors.New("same Bot bindings or independent interactions were merged")
	}
	for _, item := range []struct {
		run interactionRun
		sum int64
	}{{parallel, 17}, {second, 23}} {
		done, e := d.interactionWait(f.requester, item.run.ID, "succeeded")
		if e != nil {
			return e
		}
		computed, e := interactionDecodedResult(done)
		if e != nil {
			return e
		}
		if computed.Sum != item.sum || len(computed.Materials) != 0 {
			return errors.New("parallel builtin context included another work or recent chat")
		}
		if e = f.checkPublished(done.publicRun, f.requester); e != nil {
			return e
		}
		if e = f.evidence(fmt.Sprintf("builtin_parallel_%d", item.sum), done.publicRun); e != nil {
			return e
		}
	}
	// Private file data is observed in real provider input, never shared output.
	privateCanary := "CUTOVER_PRIVATE_" + f.suffix
	file, err := d.attachmentUpload(f.requester, "cutover-private-"+f.suffix+".txt", "text/plain", []byte(privateCanary+" VALUE=101"), 1)
	if err != nil {
		return err
	}
	grant, err := d.resourceGrant(f.requester, f.bot.ID, f.conv, "file", file.info.FileID, "personal", false)
	if err != nil {
		return err
	}
	privateToken := "INTERACTION_CUTOVER_PRIVATE_" + f.suffix
	private, err := d.interactionStart(f.requester, f.bot.ID, f.conv, privateToken+" VALUE=2", map[string]any{"visibility": "personal", "resource_grant_ids": []entityID{grant.ID}, "file_ids": []entityID{file.info.FileID}})
	if err != nil {
		return err
	}
	privateRun, err := d.waitRun(f.requester, private.ID)
	if err != nil {
		return err
	}
	var privateResult interactionResult
	if err = json.Unmarshal([]byte(privateRun.ResultText), &privateResult); err != nil {
		return err
	}
	if privateRun.Status != "succeeded" || privateRun.Visibility != "personal" || privateRun.MessageID != nil || privateResult.Sum != 103 || !strings.Contains(privateRun.ResultText, privateCanary) {
		return errors.New("private builtin work failed real authorized read or published to group")
	}
	for _, caller := range []account{f.owner, f.admin, f.member} {
		if err = d.runtimeReject(http.MethodGet, "/runs/"+private.ID.String(), caller, nil, http.StatusForbidden); err != nil {
			return err
		}
	}
	privateRequests, err := d.interactionRequests(privateToken)
	if err != nil {
		return err
	}
	if len(privateRequests) == 0 || !strings.Contains(string(privateRequests[0]["messages"]), privateCanary) {
		return errors.New("private resource was not observed in actual provider request")
	}
	if err = f.evidence("builtin_private", privateRun); err != nil {
		return err
	}
	// An accepted source is edited and recalled while its builtin work waits.
	for _, operation := range []string{"edit", "recall"} {
		if err = d.mutateWorkSource(f.requester, f.admin, source, operation); err != nil {
			return err
		}
	}
	if err = f.checkRecalled(source); err != nil {
		return err
	}
	current, err := d.readRun(f.admin, waiting.ID)
	if err != nil {
		return err
	}
	if current.InputVersion != waiting.InputVersion || current.StopStatus != "none" || current.ContextNotice != "" {
		return errors.New("ordinary IM mutation changed accepted work")
	}
	if err = d.interactionGate(token, "arm"); err != nil {
		return err
	}
	defer func() { result = errors.Join(result, d.interactionGate(token, "release")) }()
	var receipts [2]interactionRun
	var failures [2]error
	callers := []account{f.requester, f.admin}
	texts := []string{"CUTOVER_FIRST_" + f.suffix + " VALUE=7", "CUTOVER_SECOND_" + f.suffix + " VALUE=11"}
	var wg sync.WaitGroup
	for i := range receipts {
		wg.Go(func() {
			receipts[i], failures[i] = d.interactionControl(callers[i], waiting.ID, "input", texts[i], uuid.NewString(), nil)
		})
	}
	wg.Wait()
	for _, failure := range failures {
		if failure != nil {
			return failure
		}
	}
	versions := []int64{receipts[0].InputVersion, receipts[1].InputVersion}
	slices.Sort(versions)
	if !slices.Equal(versions, []int64{2, 3}) {
		return errors.New("joint inputs were overwritten or not serialized")
	}
	held, err := d.interactionHeldRequest(token)
	if err != nil {
		return err
	}
	thirdText := "CUTOVER_DURING_MODEL_" + f.suffix + " VALUE=13"
	if _, err = d.interactionControl(f.admin, waiting.ID, "input", thirdText, uuid.NewString(), nil); err != nil {
		return err
	}
	if strings.Contains(string(held["messages"]), thirdText) {
		return errors.New("in-flight request snapshot included later accepted input")
	}
	// Disconnect one human before the final result, not the executor or provider.
	before, err := d.inboxRead("cutover-builtin-before-offline", f.requester, 0, 100)
	if err != nil {
		return err
	}
	f.a.close()
	if err = d.interactionGate(token, "release"); err != nil {
		return err
	}
	done, err := d.interactionWait(f.admin, waiting.ID, "succeeded")
	if err != nil {
		return err
	}
	computed, err := interactionDecodedResult(done)
	if err != nil {
		return err
	}
	if computed.Sum != 39 || !slices.Contains(computed.Contributors, f.requester.id) || !slices.Contains(computed.Contributors, f.admin.id) || !strings.Contains(done.ResultText, source.Content.Text) {
		return errors.New("joint result lost accepted source or contributor amounts")
	}
	requests, err := d.interactionRequests(token)
	if err != nil {
		return err
	}
	if len(requests) < 2 {
		return errors.New("no actual resumed model requests")
	}
	var previous []map[string]json.RawMessage
	for _, request := range requests {
		_, messages, e := interactionInputs(request)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(request["tools"], requests[0]["tools"]) || len(previous) > len(messages) || previous != nil && !reflect.DeepEqual(previous, messages[:len(previous)]) {
			return errors.New("same branch model prefix or accepted trajectory was rewritten")
		}
		for _, excluded := range []string{unrelated, privateCanary, "INTERACTION_CUTOVER_PARALLEL_", "INTERACTION_CUTOVER_SECOND_", "EDITED_NOT_ACCEPTED_"} {
			if strings.Contains(string(request["messages"]), excluded) {
				return errors.New("provider input mixed parallel/private/recent/edited source material")
			}
		}
		previous = messages
	}
	f.a, err = d.p6Connect(f.secondAddress, f.requester)
	if err != nil {
		return err
	}
	restored, err := d.readRun(f.requester, done.ID)
	if err != nil {
		return err
	}
	if restored.Version < done.Version || restored.ResultText != done.ResultText || !hasEntityID(restored.MessageID, *done.MessageID) {
		return errors.New("reconnected authoritative Run regressed or lost final result")
	}
	if err = f.checkPublished(restored, f.requester); err != nil {
		return err
	}
	if err = f.catchup(f.requester, before.NextPosition, []publicRun{restored}, privateCanary); err != nil {
		return err
	}
	if err = f.evidence("builtin_reconnected", restored); err != nil {
		return err
	}
	continued, err := d.interactionStart(f.admin, f.bot.ID, f.conv, "INTERACTION_CUTOVER_CONTINUE_"+f.suffix+" VALUE=19", map[string]any{"parent_run_id": done.ID})
	if err != nil {
		return err
	}
	if continued.ID == done.ID || continued.InteractionID != done.InteractionID || !hasEntityID(continued.ParentRunID, done.ID) {
		return errors.New("explicit continuation lost relation to independent accepted work")
	}
	continued, err = d.interactionWait(f.admin, continued.ID, "succeeded")
	if err != nil {
		return err
	}
	continuation, err := interactionDecodedResult(continued)
	if err != nil {
		return err
	}
	hasParentResult := false
	for _, material := range continuation.Materials {
		if material.ParentRunID == done.ID && strings.Contains(material.Content, `"sum":39`) {
			hasParentResult = true
		}
	}
	// The continuation carries accepted parent inputs (36), the retained
	// reference (3), its copy inside the parent result (3), and new input (19).
	if continuation.Sum != 61 || !hasParentResult {
		return errors.New("explicit continuation did not retain accepted source snapshot after recall")
	}
	if err = f.checkPublished(continued.publicRun, f.admin); err != nil {
		return err
	}
	if err = d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.admin.token, map[string]any{"conv_id": f.conv, "submission_key": uuid.NewString(), "message": "cannot reread source", "message_ids": []entityID{source.MessageID}}); err != nil {
		return err
	}
	if err = f.groupNoCanary(privateCanary); err != nil {
		return err
	}
	return f.evidence("builtin_explicit_continuation", continued.publicRun)
}

// Message history and inbox recover messages; Run versions recover work. These
// clocks are intentionally never compared or copied into one another.
func (f *cutoverFixture) checkPublished(run publicRun, caller account) error {
	if run.Status != "succeeded" || run.OutputStatus != "committed" || run.MessageID == nil {
		return errors.New("result not authoritatively committed")
	}
	message, err := f.d.identityMessage(caller, *run.MessageID)
	if err != nil {
		return err
	}
	if message.SenderType != "bot" || !hasEntityID(message.SenderID, run.BotID) || message.ConvID != run.ConvID || message.Text.Text != run.ResultText {
		return errors.New("stored message identity/content differs from authoritative Run")
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err = f.d.request(http.MethodGet, "/convs/"+run.ConvID.String()+"/messages?limit=100", caller.token, nil, &history); err != nil {
		return err
	}
	count := 0
	for _, item := range history.Messages {
		if item.MessageID == *run.MessageID || item.Text.Text == run.ResultText && hasEntityID(item.SenderID, run.BotID) {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("independent result has %d history entries, expected one", count)
	}
	return nil
}

func (f *cutoverFixture) catchup(caller account, position sequenceNumber, runs []publicRun, excluded string) error {
	pending := map[entityID]publicRun{}
	for _, run := range runs {
		if run.MessageID == nil {
			return errors.New("inbox expected result lacks committed message identity")
		}
		pending[*run.MessageID] = run
	}
	seen := map[entityID]bool{}
	deadline := time.Now().Add(f.d.timeout)
	for time.Now().Before(deadline) {
		page, err := f.d.inboxRead("cutover-reconnect", caller, position, 100)
		if err != nil {
			return err
		}
		if page.RebuildRequired {
			return errors.New("known inbox cursor unexpectedly required rebuild")
		}
		for _, change := range page.Changes {
			if excluded != "" && strings.Contains(string(page.raw), excluded) {
				return errors.New("private content entered public inbox")
			}
			run, ok := pending[change.Message.MessageID]
			if !ok {
				if change.Kind == "message.new" && seen[change.Message.MessageID] {
					return errors.New("result replay duplicated inbox message")
				}
				continue
			}
			message := change.Message
			if change.Kind != "message.new" || message.SenderType != "bot" || !hasEntityID(message.SenderID, run.BotID) || message.Text.Text != run.ResultText || message.ConvID != run.ConvID {
				return errors.New("inbox message does not match authoritative independent result")
			}
			seen[message.MessageID] = true
			delete(pending, message.MessageID)
		}
		position = page.NextPosition
		if !page.HasMore && len(pending) == 0 {
			fmt.Printf("CUTOVER public: inbox recovered=%d next_position=%s\n", len(seen), position)
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("inbox reconnect missing %d committed result(s)", len(pending))
}

func (f *cutoverFixture) ruleRecoveryComposite() error {
	d := f.d
	x, err := d.websocketExecutor(f.owner, f.external.ID, f.address, "cutover-rule-old-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	ruleID := uuid.NewString()
	match := "CUTOVER_RULE_" + f.suffix
	settings, err := json.Marshal(map[string]any{"activity_types": []string{"message.created"}, "response_rules": []map[string]any{{"id": ruleID, "enabled": true, "activity_type": "message.created", "message_contains": match}}})
	if err != nil {
		return err
	}
	if err = d.activitySettings(f.admin, f.conv, f.external.ID, string(settings)); err != nil {
		return err
	}
	source, err := d.sendMessage(f.requester, f.conv, match+" accepted automatic shared work", 0)
	if err != nil {
		return err
	}
	var automatic publicRun
	if err = d.broadcastPoll("cutover automatic rule", func(b *driver) (bool, error) {
		runs, e := b.conversationRuns(f.admin, f.conv)
		if e != nil {
			return false, e
		}
		for _, run := range runs {
			if run.Activity != nil && run.Activity.MessageID == source.MessageID && run.BotID == f.external.ID {
				automatic = run
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	if automatic.TriggerKind != "rule" || automatic.RequesterID != "" || automatic.Origin == nil || automatic.Origin.RuleID.String() != ruleID || automatic.Origin.AuthorizationUserID != f.admin.id {
		return errors.New("automatic shared work fabricated user or lost rule authority")
	}
	request, err := x.request(automatic.ID)
	if err != nil {
		return err
	}
	if !strings.Contains(request.Text(), source.Content.Text) {
		return errors.New("rule input not delivered to actual external executor")
	}
	if _, err = x.update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	if _, err = x.update(executorPayload(request, "run.update", "running", 1), false, false); err != nil {
		return err
	}
	path := "/bots/" + f.external.ID.String() + "/executor/actions"
	actionToken := "CUTOVER_ACTION_" + f.suffix
	var action authorizationAction
	actionBody := map[string]any{"op": "request", "run_id": automatic.ID, "generation": request.Generation, "input_version": request.InputVersion, "submission_key": uuid.NewString(), "server_id": f.server, "tool_name": "fixture_auth_send", "arguments": map[string]string{"target": "isolated-cutover-ledger", "text": actionToken + "|" + request.Text()}}
	if err = d.request(http.MethodPost, path, x.token, actionBody, &action); err != nil {
		return err
	}
	execute := actionDecision(action, "execute", uuid.NewString())
	if err = d.authorizationReject(path, x.token, execute); err != nil {
		return err
	}
	if err = f.sideEffects(actionToken, 0); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bot-authorizations/actions", f.owner.token, actionDecision(action, "approve", uuid.NewString()), nil); err != nil {
		return err
	}
	for _, operation := range []string{"edit", "recall"} {
		if err = d.mutateWorkSource(f.requester, f.admin, source, operation); err != nil {
			return err
		}
	}
	current, err := d.readRun(f.admin, automatic.ID)
	if err != nil {
		return err
	}
	if current.InputVersion != automatic.InputVersion || current.StopStatus != "none" || current.ContextNotice != "" {
		return errors.New("source operation cancelled accepted rule/action work")
	}
	var outcome struct {
		Result string `json:"result"`
	}
	for range 2 {
		if err = d.request(http.MethodPost, path, x.token, execute, &outcome); err != nil {
			return err
		}
	}
	if !strings.Contains(outcome.Result, actionToken) || !strings.Contains(outcome.Result, source.Content.Text) {
		return errors.New("approved external action did not use actual accepted rule source")
	}
	if err = f.sideEffects(actionToken, 1); err != nil {
		return err
	}
	before, err := d.inboxRead("cutover-rule-before-offline", f.admin, 0, 100)
	if err != nil {
		return err
	}
	f.b.close()
	if err = x.conn.Close(); err != nil {
		return err
	}
	r := recoveryFixture{authorizationFixture: f.authorizationFixture}
	lost, err := r.wait(automatic.ID, 100*time.Second, func(run publicRun) bool { return run.ExecutorStatus == "lost" && run.Stage == "executor_lost" })
	if err != nil {
		return err
	}
	if err = f.evidence("rule_executor_lost", lost); err != nil {
		return err
	}
	resumed, err := d.interactionControl(f.admin, automatic.ID, "resume", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if resumed.ID != automatic.ID || resumed.Generation <= request.Generation || resumed.InputVersion != request.InputVersion {
		return errors.New("recovery changed identity/input or failed generation fence")
	}
	y, err := d.websocketExecutor(f.owner, f.external.ID, f.secondAddress, "cutover-rule-new-"+f.suffix)
	if err != nil {
		return err
	}
	defer y.conn.Close()
	fresh, err := y.requestVersion(automatic.ID, resumed.Generation, resumed.InputVersion, "")
	if err != nil {
		return err
	}
	if fresh.Text() != request.Text() {
		return errors.New("recovery reconstructed edited source instead of accepted work")
	}
	if _, err = y.update(executorPayload(fresh, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	late := executorPayload(request, "run.update", "completed", 2)
	late["result_text"] = "CUTOVER_STALE_RESULT_" + f.suffix
	// Use the old credential and the new one: neither may resurrect the old claim.
	x.rest = true
	if _, err = x.update(late, true, false); err != nil {
		return err
	}
	if _, err = y.update(late, true, false); err != nil {
		return err
	}
	if err = d.authorizationReject(path, y.token, execute); err != nil {
		return err
	}
	if err = d.authorizationReject(path, y.token, actionBody); err != nil {
		return err
	}
	if err = f.sideEffects(actionToken, 1); err != nil {
		return err
	}
	// A new Bot child has independent identity and receives only explicit task input.
	var childBot botView
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "cutover-child-" + f.suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "capabilities": controlCapabilities}, &childBot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": childBot.ID}, nil); err != nil {
		return err
	}
	z, err := d.websocketExecutor(f.owner, childBot.ID, f.address, "cutover-child-"+f.suffix)
	if err != nil {
		return err
	}
	defer z.conn.Close()
	childText := "independent explicitly delegated words " + f.suffix
	delegation := map[string]any{"operation": "delegate", "run_id": resumed.ID, "generation": resumed.Generation, "input_version": resumed.InputVersion, "target_bot_id": childBot.ID, "submission_key": uuid.NewString(), "message": childText}
	var child, replay publicRun
	if err = d.request(http.MethodPost, "/bot-app/delegations", y.token, delegation, &child); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/bot-app/delegations", y.token, delegation, &replay); err != nil {
		return err
	}
	if replay.ID != child.ID || child.ID == resumed.ID || child.RequesterID != "" || child.Origin == nil || child.Origin.SourceRunID != resumed.ID || child.Origin.DelegatingBotID != f.external.ID {
		return errors.New("delegation replay lost independent identity or forged user")
	}
	cr, err := z.request(child.ID)
	if err != nil {
		return err
	}
	if cr.Text() != childText {
		return errors.New("child inherited unrelated parent materials")
	}
	if _, err = z.update(executorPayload(cr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	childDone := executorPayload(cr, "run.update", "completed", 1)
	childDone["result_text"] = "cutover-child:" + executorText(cr.Text())
	if _, err = z.update(childDone, false, false); err != nil {
		return err
	}
	child, err = d.publishedRun(f.admin, child.ID)
	if err != nil {
		return err
	}
	var consumed struct {
		Run        publicRun `json:"run"`
		ResultText string    `json:"result_text"`
	}
	if err = d.request(http.MethodPost, "/bot-app/delegations", y.token, map[string]any{"operation": "result", "run_id": resumed.ID, "generation": resumed.Generation, "input_version": resumed.InputVersion, "child_run_id": child.ID}, &consumed); err != nil {
		return err
	}
	if consumed.Run.ID != child.ID || consumed.ResultText != child.ResultText {
		return errors.New("recovered parent did not consume independent child result")
	}
	finish := executorPayload(fresh, "run.update", "completed", 1)
	finish["result_text"] = "cutover-recovered:" + executorText(fresh.Text()) + "|" + consumed.ResultText
	for range 2 {
		if _, err = y.update(finish, false, false); err != nil {
			return err
		}
	}
	finished, err := d.publishedRun(f.admin, resumed.ID)
	if err != nil {
		return err
	}
	f.b, err = d.p6Connect(f.address, f.admin)
	if err != nil {
		return err
	}
	for _, run := range []publicRun{finished, child} {
		restored, e := d.readRun(f.admin, run.ID)
		if e != nil {
			return e
		}
		if restored.Version < run.Version || restored.ResultText != run.ResultText || restored.Generation != run.Generation {
			return errors.New("reconnected Run authority regressed")
		}
		if e = f.checkPublished(restored, f.admin); e != nil {
			return e
		}
	}
	if err = f.catchup(f.admin, before.NextPosition, []publicRun{finished, child}, "CUTOVER_STALE_RESULT_"+f.suffix); err != nil {
		return err
	}
	if err = f.sideEffects(actionToken, 1); err != nil {
		return err
	}
	if err = f.evidence("recovered_rule_unique_result", finished); err != nil {
		return err
	}
	return f.evidence("recovered_delegation_unique_child", child)
}

func (f *cutoverFixture) stopAndRevokeComposite() error {
	d := f.d
	x, err := d.websocketExecutor(f.owner, f.external.ID, f.address, "cutover-controls-"+f.suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	var target botView
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "cutover-stop-child-" + f.suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "capabilities": controlCapabilities}, &target); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": target.ID}, nil); err != nil {
		return err
	}
	y, err := d.websocketExecutor(f.owner, target.ID, f.secondAddress, "cutover-stop-child-"+f.suffix)
	if err != nil {
		return err
	}
	defer y.conn.Close()
	parent, err := d.startRun(f.requester, f.external.ID, f.conv, "CUTOVER_PARENT_STOP_"+f.suffix, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	pr, err := x.request(parent.ID)
	if err != nil {
		return err
	}
	if _, err = x.update(executorPayload(pr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	var child publicRun
	if err = d.request(http.MethodPost, "/bot-app/delegations", x.token, map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": pr.Generation, "input_version": pr.InputVersion, "target_bot_id": target.ID, "submission_key": uuid.NewString(), "message": "child independently refuses cancellation"}, &child); err != nil {
		return err
	}
	cr, err := y.request(child.ID)
	if err != nil {
		return err
	}
	if _, err = y.update(executorPayload(cr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+parent.ID.String()+"/control", f.member, map[string]any{"operation": "stop", "submission_key": uuid.NewString()}, http.StatusForbidden); err != nil {
		return err
	}
	taken, err := d.interactionControl(f.admin, parent.ID, "takeover", "", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if taken.RequesterID != f.requester.id || taken.ControllerID != f.admin.id {
		return errors.New("explicit takeover rewrote original request provenance")
	}
	stopping, err := d.controlExecutorRun(f.admin, parent.ID, "stop", "", uuid.NewString())
	if err != nil {
		return err
	}
	if stopping.StopStatus != "requested" || stopping.Status != "stopping" {
		return errors.New("stop request falsely confirmed actual executor cancellation")
	}
	ps, err := x.requestVersion(parent.ID, pr.Generation, pr.InputVersion, "stopping")
	if err != nil {
		return err
	}
	cs, err := y.requestVersion(child.ID, cr.Generation, cr.InputVersion, "stopping")
	if err != nil {
		return err
	}
	if _, err = x.update(executorPayload(ps, "run.update", "stopped", 1), false, false); err != nil {
		return err
	}
	if _, err = y.update(executorPayload(cs, "run.update", "stop_rejected", 1), false, false); err != nil {
		return err
	}
	stopped, err := d.readRun(f.admin, parent.ID)
	if err != nil {
		return err
	}
	refused, err := d.readRun(f.admin, child.ID)
	if err != nil {
		return err
	}
	if stopped.StopStatus != "confirmed" || stopped.Status != "stopped" || refused.StopStatus != "rejected" || refused.Status == "stopped" {
		return errors.New("parent/child stop collapsed distinct executor outcomes")
	}
	late := executorPayload(pr, "run.update", "completed", 2)
	late["result_text"] = "CUTOVER_STOPPED_MUST_NOT_PUBLISH"
	if _, err = x.update(late, true, false); err != nil {
		return err
	}
	if err = d.authorizationReject("/bot-app/delegations", x.token, map[string]any{"operation": "delegate", "run_id": parent.ID, "generation": pr.Generation, "input_version": pr.InputVersion, "target_bot_id": target.ID, "submission_key": uuid.NewString(), "message": "late child"}); err != nil {
		return err
	}
	// The child really refused stop: finish it independently rather than hide it.
	childCompletion := executorPayload(cr, "run.update", "completed", 2)
	childCompletion["result_text"] = "cutover-refused-child:" + executorText(cr.Text())
	if _, err = y.update(childCompletion, false, false); err != nil {
		return err
	}
	child, err = d.publishedRun(f.admin, child.ID)
	if err != nil {
		return err
	}
	if err = f.checkPublished(child, f.admin); err != nil {
		return err
	}
	if err = f.evidence("parent_stop_confirmed", stopped); err != nil {
		return err
	}
	if err = f.evidence("child_stop_refused_completed", child); err != nil {
		return err
	}
	// A real resource grant is different from IM hiding: revoked reads and old
	// result submissions must now fail at the public executor boundary.
	canary := "CUTOVER_REVOKED_RESOURCE_" + f.suffix
	file, err := d.attachmentUpload(f.requester, "cutover-revoke-"+f.suffix+".txt", "text/plain", []byte(canary), 3)
	if err != nil {
		return err
	}
	grant, err := d.resourceGrant(f.requester, f.external.ID, f.conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	run, err := d.interactionStart(f.requester, f.external.ID, f.conv, "CUTOVER_REAL_REVOKE_"+f.suffix, map[string]any{"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{grant.ID}})
	if err != nil {
		return err
	}
	rr, err := x.request(run.ID)
	if err != nil {
		return err
	}
	if _, err = x.update(executorPayload(rr, "run.claim", "accepted", 0), false, false); err != nil {
		return err
	}
	resourcePath := "/bots/" + f.external.ID.String() + "/executor/resources"
	read := map[string]any{"op": "read", "run_id": run.ID, "grant_id": grant.ID, "generation": rr.Generation, "input_version": rr.InputVersion}
	var bytes json.RawMessage
	if err = d.request(http.MethodPost, resourcePath, x.token, read, &bytes); err != nil {
		return err
	}
	if !strings.Contains(string(bytes), canary) {
		return errors.New("real grant did not permit actual external resource read")
	}
	if err = d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
		return err
	}
	if err = d.authorizationReject(resourcePath, x.token, read); err != nil {
		return err
	}
	completion := executorPayload(rr, "run.update", "completed", 1)
	completion["result_text"] = canary
	if _, err = x.update(completion, true, false); err != nil {
		return err
	}
	if err = f.groupNoCanary(canary); err != nil {
		return err
	}
	// Replays still authorize the resource, even with an already known Run key.
	staleRead := maps.Clone(read)
	if err = d.authorizationReject(resourcePath, x.token, staleRead); err != nil {
		return err
	}
	if err = d.runtimeReject(http.MethodGet, "/runs/"+run.ID.String(), f.admin, nil, http.StatusForbidden); err != nil {
		return err
	}
	fmt.Printf("CUTOVER public: phase=real_resource_revoked run=%s read_forbidden=true result_submission_rejected=true external_read_replay_rejected=true no_group_disclosure=true\n", run.ID)
	return nil
}

func (f *cutoverFixture) groupNoCanary(canary string) error {
	// Conversation membership does not grant access to another user's private work.
	for _, caller := range []account{f.requester, f.admin, f.member} {
		for _, conv := range []entityID{f.conv, f.otherConv} {
			var history json.RawMessage
			if err := f.d.request(http.MethodGet, "/convs/"+conv.String()+"/messages?limit=100", caller.token, nil, &history); err != nil {
				return err
			}
			if strings.Contains(string(history), canary) {
				return errors.New("restricted material leaked into persisted group history")
			}
			page, err := f.d.inboxRead("cutover-private-isolation", caller, 0, 100)
			if err != nil {
				return err
			}
			if strings.Contains(string(page.raw), canary) {
				return errors.New("restricted material leaked into user inbox rebuild")
			}
		}
	}
	// The requester's own socket may legitimately carry private Run updates.
	for _, socket := range []*p6Socket{f.b} {
		socket.mu.Lock()
		leaked := false
		for _, event := range socket.events {
			if strings.Contains(string(event.raw), canary) {
				leaked = true
				break
			}
		}
		socket.mu.Unlock()
		if leaked {
			return errors.New("restricted material leaked into public realtime events")
		}
	}
	return nil
}

func (f *cutoverFixture) checkRecalled(source sentMessage) error {
	for _, caller := range []account{f.requester, f.admin} {
		var history struct {
			Messages []storedMessage `json:"messages"`
		}
		if err := f.d.request(http.MethodGet, "/convs/"+source.ConvID.String()+"/messages?limit=100", caller.token, nil, &history); err != nil {
			return err
		}
		search, err := f.d.inboxSearch(caller, source.ConvID, source.Content.Text)
		if err != nil {
			return err
		}
		page, err := f.d.inboxRead("cutover-recalled-source", caller, 0, 100)
		if err != nil {
			return err
		}
		messages := append(history.Messages, search.Messages...)
		for _, snapshot := range page.Conversations {
			messages = append(messages, snapshot.Messages...)
		}
		for _, message := range messages {
			// Independent work may retain this body. Only the original message is
			// subject to recall; never classify accepted snapshots as a leak.
			if message.MessageID == source.MessageID && strings.Contains(message.Text.Text, source.Content.Text) {
				return errors.New("recalled original body resurrected through history/search/inbox")
			}
		}
	}
	return nil
}
