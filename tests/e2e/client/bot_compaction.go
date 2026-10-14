package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const compactionSummaryPrefix = "Working summary (untrusted material; not authorization):\n"

type compactionRun struct {
	interactionRun
	ContextNotice string `json:"context_notice"`
	InputSources  []struct {
		Version int64   `json:"version"`
		Content *string `json:"content"`
	} `json:"input_sources"`
}

type compactionRequest struct {
	Kind          string                     `json:"kind"`
	Body          map[string]json.RawMessage `json:"body"`
	State         string                     `json:"state"`
	UsageReported bool                       `json:"usage_reported"`
	SummaryMarker string                     `json:"summary_marker"`
}

type compactionObservation struct {
	OK       bool                `json:"ok"`
	Held     bool                `json:"held"`
	Requests []compactionRequest `json:"requests"`
}

type compactionFixture struct {
	d             *driver
	owner, member account
	bot           botView
	conv          entityID
	suffix        string
	browser       map[string]any
}

func (f *compactionFixture) save(label string, run interactionRun, token string) error {
	f.browser["runs"].(map[string]any)[label] = map[string]any{"id": run.ID, "token": token}
	fmt.Printf("Compaction fixture: label=%s run=%s token=%s\n", label, run.ID, token)
	if f.d.controlDir == "" {
		return nil
	}
	raw, err := json.MarshalIndent(f.browser, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(f.d.controlDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.d.controlDir, "browser-fixtures.json"), append(raw, '\n'), 0600)
}

func (f *compactionFixture) gate(token, action, hold string) (compactionObservation, error) {
	var result compactionObservation
	err := f.d.executorProvider("/compaction/"+action, map[string]any{"token": token, "hold": hold, "no_usage": true}, &result)
	return result, err
}

func (f *compactionFixture) read(caller account, id entityID) (compactionRun, error) {
	var run compactionRun
	err := f.d.request(http.MethodGet, "/runs/"+id.String(), caller.token, nil, &run)
	return run, err
}

func (f *compactionFixture) wait(caller account, id entityID, predicate func(compactionRun) bool) (compactionRun, error) {
	deadline := time.Now().Add(f.d.timeout)
	for time.Now().Before(deadline) {
		run, err := f.read(caller, id)
		if err != nil {
			return run, err
		}
		if predicate(run) {
			return run, nil
		}
		if run.Status == "failed" {
			return run, fmt.Errorf("compaction run failed: %s", run.Error)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return compactionRun{}, errors.New("compaction public state did not reach expected boundary")
}

func (f *compactionFixture) input(caller account, id entityID, text string, extra map[string]any) (interactionRun, error) {
	return f.d.interactionControl(caller, id, "input", text, uuid.NewString(), extra)
}

func (f *compactionFixture) driveSummary(run interactionRun, token string) (compactionObservation, int, int64, error) {
	sum := 5
	for _, value := range []int{7, 11, 19, 23} {
		receipt, err := f.input(f.member, run.ID, fmt.Sprintf("SUPPLEMENT_%d", value)+compactionPadding(value), nil)
		if err != nil {
			return compactionObservation{}, sum, 0, err
		}
		sum += value
		deadline := time.Now().Add(f.d.timeout)
		for time.Now().Before(deadline) {
			observed, err := f.gate(token, "observe", "")
			if err != nil {
				return observed, sum, 0, err
			}
			if observed.Held {
				return observed, sum, receipt.InputVersion, nil
			}
			current, err := f.read(f.owner, run.ID)
			if err != nil {
				return observed, sum, 0, err
			}
			if current.Status == "failed" {
				return observed, sum, 0, fmt.Errorf("compaction never reached summary: %s", current.Error)
			}
			if current.Status == "waiting_input" && current.InputVersion == receipt.InputVersion {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	return compactionObservation{}, sum, 0, errors.New("budget never produced actual summary request")
}

func compactionPadding(value int) string {
	// Distinct actual accepted work; not provider-reported token counts forcing budget.
	return fmt.Sprintf(" VALUE=%d ", value) + strings.Repeat("work-detail ", 280)
}

func compactionMessages(request compactionRequest) ([]map[string]json.RawMessage, error) {
	var messages []map[string]json.RawMessage
	err := json.Unmarshal(request.Body["messages"], &messages)
	return messages, err
}

func compactionView(request compactionRequest) (string, int, error) {
	messages, err := compactionMessages(request)
	if err != nil {
		return "", -1, err
	}
	for i, item := range messages {
		var content, role string
		_ = json.Unmarshal(item["content"], &content)
		_ = json.Unmarshal(item["role"], &role)
		if strings.HasPrefix(content, compactionSummaryPrefix) {
			if role != "user" {
				return "", -1, errors.New("compaction summary became privileged system/assistant material")
			}
			return content, i, nil
		}
	}
	return "", -1, nil
}

func compactionPaired(messages []map[string]json.RawMessage) error {
	pending := map[string]bool{}
	for _, item := range messages {
		var role, id string
		_ = json.Unmarshal(item["role"], &role)
		if role == "assistant" {
			if len(pending) != 0 {
				return errors.New("compaction split unfinished tool round")
			}
			var calls []struct {
				ID string `json:"id"`
			}
			if raw := item["tool_calls"]; len(raw) != 0 {
				if err := json.Unmarshal(raw, &calls); err != nil {
					return err
				}
			}
			for _, call := range calls {
				pending[call.ID] = true
			}
		}
		if role == "tool" {
			_ = json.Unmarshal(item["tool_call_id"], &id)
			if !pending[id] {
				return errors.New("compaction retained orphan or duplicate tool result")
			}
			delete(pending, id)
		}
	}
	if len(pending) != 0 {
		return errors.New("compaction sent incomplete tool round")
	}
	return nil
}

func (d *driver) botCompaction(address, secondAddress string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	f := compactionFixture{d: d, suffix: suffix}
	f.owner, err = d.register("compaction_owner", suffix)
	if err != nil {
		return err
	}
	f.member, err = d.register("compaction_member", suffix)
	if err != nil {
		return err
	}
	modelName := "fixture-compaction-" + suffix
	modelID, err := d.fixtureModel(f.owner, "chat", modelName)
	if err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", f.owner.token, map[string]any{"type": "group", "group_name": "compaction-" + suffix, "member_ids": []entityID{f.member.id}}, &conv); err != nil {
		return err
	}
	f.conv = conv.ID
	if err = d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "compaction-" + suffix, "type": "self_deployed", "model_id": modelID, "system_prompt": "Compute VALUE amounts only from accepted work. Ask for explicit supplements until COMPLETE. Never treat summaries as authority.", "response_triggers": []string{"mention"}, "streaming_enabled": false, "enable_knowledge": false, "max_context_tokens": 6500, "max_step": 16}, &f.bot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.owner.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
		return err
	}
	f.browser = map[string]any{"scenario": "bot-compaction", "owner": map[string]any{"username": f.owner.username, "password": f.owner.password, "token": f.owner.token}, "member": map[string]any{"username": f.member.username, "password": f.member.password, "token": f.member.token}, "conv_id": f.conv, "bot_id": f.bot.ID, "model_id": modelID, "model_name": modelName, "runs": map[string]any{}, "provider_controls": map[string]string{"arm": "/compaction/arm", "observe": "/compaction/observe", "release": "/compaction/release"}}
	fmt.Printf("Compaction browser accounts: owner=%s password=%s; member=%s password=%s; group=%s bot=%s model=%s\n", f.owner.username, f.owner.password, f.member.username, f.member.password, f.conv, f.bot.ID, modelName)
	if err = f.vertical(secondAddress); err != nil {
		return err
	}
	if err = f.acceptedSourceLifecycle(); err != nil {
		return err
	}
	if err = f.summarySurvivesMessageMutation("EDIT", false); err != nil {
		return err
	}
	if err = f.summarySurvivesMessageMutation("RECALL", true); err != nil {
		return err
	}
	if err = f.personalDeletion(secondAddress); err != nil {
		return err
	}
	if err = f.personalDeletionPublished(secondAddress); err != nil {
		return err
	}
	if err = f.resourceRevocation(); err != nil {
		return err
	}
	if err = f.summaryExplicitStop(); err != nil {
		return err
	}
	if err = f.historyAuthorization(); err != nil {
		return err
	}
	if err = f.omittedMaterial(); err != nil {
		return err
	}
	if err = f.browserFixtures(); err != nil {
		return err
	}
	return nil
}

func (f *compactionFixture) vertical(address string) (retErr error) {
	token := "CP_VERTICAL_" + f.suffix
	if _, err := f.gate(token, "arm", "summary"); err != nil {
		return err
	}
	defer func() {
		if _, err := f.gate(token, "release", ""); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("compaction release %s: %w", token, err))
		}
	}()
	socket, err := f.d.p6Connect(address, f.member)
	if err != nil {
		return err
	}
	defer socket.close()
	source, err := f.d.sendMessage(f.owner, f.conv, "CANARY_RESTART_ACCEPTED", 0)
	if err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+compactionPadding(5), map[string]any{"message_ids": []entityID{source.MessageID}})
	if err != nil {
		return err
	}
	if err = f.save("vertical", run, token); err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.member, run.ID, "waiting_input"); err != nil {
		return err
	}
	if _, err = f.d.p6Wait(socket, 0, "compaction-wait", func(e p6Event) bool {
		return e.Type == "bot.run.updated" && e.Run.ID == run.ID && e.Run.Status == "waiting_input"
	}); err != nil {
		return err
	}
	held, sum, upper, err := f.driveSummary(run, token)
	if err != nil {
		return err
	}
	if len(held.Requests) == 0 || held.Requests[len(held.Requests)-1].Kind != "summary" {
		return errors.New("compaction budget did not reach actual summary request")
	}
	const inflight = "DURING_COMPACTION VALUE=13"
	if strings.Contains(string(held.Requests[len(held.Requests)-1].Body["messages"]), inflight) {
		return errors.New("compaction immutable upper bound included future work")
	}
	receipt, err := f.input(f.member, run.ID, inflight, nil)
	if err != nil {
		return err
	}
	if receipt.InputVersion != upper+1 {
		return errors.New("compaction concurrent accepted input was lost")
	}
	// Mutate all IM projections while the original snapshot is being compressed.
	if err = f.d.request(http.MethodPut, "/messages/"+source.MessageID.String(), f.owner.token, map[string]any{"text": "CANARY_RESTART_NOT_ACCEPTED"}, nil); err != nil {
		return err
	}
	if err = f.d.request(http.MethodDelete, "/messages/"+source.MessageID.String(), f.member.token, map[string]any{"delete_for_all": false}, nil); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/messages/"+source.MessageID.String()+"/recall", f.owner.token, map[string]any{}, nil); err != nil {
		return err
	}
	if err = f.d.request(http.MethodDelete, "/messages/"+source.MessageID.String(), f.owner.token, map[string]any{"delete_for_all": true}, nil); err != nil {
		return err
	}
	if _, err = f.gate(token, "release", ""); err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.member, run.ID, "waiting_input"); err != nil {
		return err
	}
	before, err := f.gate(token, "observe", "")
	if err != nil {
		return err
	}
	var last compactionRequest
	for _, row := range before.Requests {
		if row.Kind == "work" {
			last = row
		}
	}
	view, _, err := compactionView(last)
	if err != nil {
		return err
	}
	if view == "" {
		return errors.New("compaction never published usable summary+tail view")
	}
	// Stop/recreate the actual runtime while waiting; no active tool/model is repeated.
	if f.d.controlDir != "" {
		if err = f.d.p6Checkpoint("compaction-restart"); err != nil {
			return err
		}
		// Process health is not readiness of the forwarding RPC connection. Probe
		// the user's read-only route before submitting a non-replayed mutation.
		deadline := time.Now().Add(f.d.timeout)
		for {
			restored, readErr := f.read(f.owner, run.ID)
			if readErr == nil {
				if restored.Status != "waiting_input" {
					return fmt.Errorf("restarted work unexpectedly advanced to %s", restored.Status)
				}
				break
			}
			var unavailable *apiError
			if !errors.As(readErr, &unavailable) || unavailable.status < 500 || unavailable.status >= 600 || !time.Now().Before(deadline) {
				return readErr
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	if _, err = f.input(f.owner, run.ID, "COMPLETE VALUE=17", nil); err != nil {
		return err
	}
	done, err := f.d.interactionWait(f.owner, run.ID, "succeeded")
	if err != nil {
		return err
	}
	var result struct {
		Sum       int      `json:"sum"`
		Versions  []int    `json:"versions"`
		Materials []string `json:"materials"`
	}
	if err = json.Unmarshal([]byte(done.ResultText), &result); err != nil {
		return err
	}
	expectedVersions := make([]int, int(upper)+2)
	for i := range expectedVersions {
		expectedVersions[i] = i + 1
	}
	if result.Sum != sum+30 || !slices.Equal(result.Versions, expectedVersions) {
		return fmt.Errorf("compaction sum/version evidence lost or duplicated fixed-watermark inputs: %+v", result)
	}
	if !slices.Contains(result.Materials, "CANARY_RESTART_ACCEPTED") || slices.Contains(result.Materials, "CANARY_RESTART_NOT_ACCEPTED") {
		return errors.New("runtime restart did not retain original accepted snapshot")
	}
	observed, err := f.gate(token, "observe", "")
	if err != nil {
		return err
	}
	seenSummary := false
	for _, row := range observed.Requests {
		if row.Kind != "work" {
			continue
		}
		messages, err := compactionMessages(row)
		if err != nil {
			return err
		}
		if err = compactionPaired(messages); err != nil {
			return err
		}
		if row.UsageReported {
			return errors.New("compaction missing-usage fixture unexpectedly supplied usage")
		}
		current, _, err := compactionView(row)
		if err != nil {
			return err
		}
		if current != "" {
			seenSummary = true
		}
	}
	if !seenSummary {
		return errors.New("compaction summary request never became work material")
	}
	fmt.Printf("Compaction evidence: actual stable prefix -> held summary + concurrent input -> stable summary/tail -> runtime restart -> sum=%d versions=1..%d; missing usage, no KV-hit claim\n", result.Sum, len(expectedVersions))
	if err = f.save("vertical_complete", done, token); err != nil {
		return err
	}
	return f.boundaries(done, token)
}

func (f *compactionFixture) mentionStart(token string, value int) (interactionRun, sentMessage, error) {
	message, err := f.d.sendBotMention(f.owner, f.bot.ID, f.conv, fmt.Sprintf("%s CANARY_REQUEST_ORIGINAL VALUE=%d", token, value))
	if err != nil {
		return interactionRun{}, message, err
	}
	var found interactionRun
	err = f.d.broadcastPoll("compaction request mention accepted", func(*driver) (bool, error) {
		runs, err := f.d.conversationRuns(f.owner, f.conv)
		if err != nil {
			return false, err
		}
		for _, run := range runs {
			if !hasEntityID(run.SourceMessageID, message.MessageID) {
				continue
			}
			current, err := f.read(f.owner, run.ID)
			if err != nil {
				return false, err
			}
			found = current.interactionRun
			return true, nil
		}
		return false, nil
	})
	return found, message, err
}

func (f *compactionFixture) acceptedSourceLifecycle() error {
	token := "CP_SOURCE_" + f.suffix
	if _, err := f.gate(token, "arm", ""); err != nil {
		return err
	}
	run, source, err := f.mentionStart(token, 5)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPut, "/messages/"+source.MessageID.String(), f.owner.token, map[string]any{"text": "CANARY_REQUEST_EDITED VALUE=1000"}, nil); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/messages/"+source.MessageID.String()+"/recall", f.owner.token, map[string]any{}, nil); err != nil {
		return err
	}
	if _, err = f.input(f.owner, run.ID, "COMPLETE VALUE=7", nil); err != nil {
		return err
	}
	done, err := f.d.interactionWait(f.owner, run.ID, "succeeded")
	if err != nil {
		return err
	}
	if !strings.Contains(done.ResultText, "\"sum\": 12") {
		return fmt.Errorf("message mutation changed accepted request: %s", done.ResultText)
	}
	if done.MessageID == nil {
		return errors.New("accepted request never published independent result")
	}
	published, err := f.d.identityMessage(f.member, *done.MessageID)
	if err != nil {
		return err
	}
	if published.Text.Text != done.ResultText {
		return errors.New("withdrawal removed independent result")
	}
	view, err := f.read(f.member, run.ID)
	if err != nil {
		return err
	}
	if len(view.InputSources) != 2 || view.InputSources[0].Content == nil || *view.InputSources[0].Content != source.Content.Text || view.InputSources[1].Content == nil || *view.InputSources[1].Content != "COMPLETE VALUE=7" {
		return errors.New("public accepted input view substituted current/withdrawn IM content")
	}
	observed, err := f.gate(token, "observe", "")
	if err != nil {
		return err
	}
	for _, request := range observed.Requests {
		if strings.Contains(string(request.Body["messages"]), "CANARY_REQUEST_EDITED") {
			return errors.New("IM edit silently entered accepted work")
		}
	}
	return f.save("source_edit", done, token)
}

func (f *compactionFixture) summarySurvivesMessageMutation(label string, recall bool) (retErr error) {
	token := "CP_" + label + "_" + f.suffix
	if _, err := f.gate(token, "arm", "summary"); err != nil {
		return err
	}
	defer func() {
		if _, err := f.gate(token, "release", ""); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("compaction release %s: %w", token, err))
		}
	}()
	canary := "CANARY_" + label + "_OLD"
	source, err := f.d.sendMessage(f.owner, f.conv, canary+" VALUE=99", 0)
	if err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+compactionPadding(5), map[string]any{"message_ids": []entityID{source.MessageID}})
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	held, sum, upper, err := f.driveSummary(run, token)
	if err != nil {
		return err
	}
	if recall {
		err = f.d.request(http.MethodPost, "/messages/"+source.MessageID.String()+"/recall", f.owner.token, map[string]any{}, nil)
	} else {
		err = f.d.request(http.MethodPut, "/messages/"+source.MessageID.String(), f.owner.token, map[string]any{"text": "CANARY_EDIT_NEW VALUE=777"}, nil)
	}
	if err != nil {
		return err
	}
	if _, err = f.input(f.member, run.ID, "DURING_MESSAGE_MUTATION VALUE=13", nil); err != nil {
		return err
	}
	if _, err = f.gate(token, "release", ""); err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	if _, err = f.input(f.owner, run.ID, "COMPLETE VALUE=17", nil); err != nil {
		return err
	}
	done, err := f.d.interactionWait(f.owner, run.ID, "succeeded")
	if err != nil {
		return err
	}
	var result struct {
		Sum       int      `json:"sum"`
		Versions  []int    `json:"versions"`
		Materials []string `json:"materials"`
	}
	if err = json.Unmarshal([]byte(done.ResultText), &result); err != nil {
		return err
	}
	if result.Sum != sum+30 || !slices.Contains(result.Materials, canary) || len(result.Versions) != int(upper)+2 {
		return fmt.Errorf("message mutation lost accepted summary facts/watermark: %+v", result)
	}
	for i, version := range result.Versions {
		if version != i+1 {
			return errors.New("message mutation duplicated/skipped accepted input")
		}
	}
	observed, err := f.gate(token, "observe", "")
	if err != nil {
		return err
	}
	used := false
	for _, row := range observed.Requests[len(held.Requests):] {
		if strings.Contains(string(row.Body["messages"]), "CANARY_EDIT_NEW") {
			return errors.New("edited reference replaced work snapshot")
		}
		if row.Kind == "work" {
			view, _, err := compactionView(row)
			if err != nil {
				return err
			}
			used = used || strings.Contains(view, held.Requests[len(held.Requests)-1].SummaryMarker)
			messages, err := compactionMessages(row)
			if err != nil {
				return err
			}
			if err = compactionPaired(messages); err != nil {
				return err
			}
		}
	}
	if !used {
		return errors.New("IM operation rejected in-flight summary checkpoint")
	}
	if err = f.save("held_"+strings.ToLower(label), done, token); err != nil {
		return err
	}
	return f.supplementWithdrawal(label)
}

func (f *compactionFixture) supplementWithdrawal(label string) error {
	token := "CP_SUPPLEMENT" + label + "_" + f.suffix
	if _, err := f.gate(token, "arm", ""); err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" VALUE=1", nil)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	canary := "CANARY_SUPPLEMENT_" + label
	source, err := f.d.sendMessage(f.member, f.conv, canary+" VALUE=9", 0)
	if err != nil {
		return err
	}
	key := uuid.NewString()
	if _, err = f.d.interactionControl(f.member, run.ID, "input", canary+" VALUE=9", key, map[string]any{"source_message_id": source.MessageID}); err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.member, run.ID, "waiting_input"); err != nil {
		return err
	}
	if err = f.d.request(http.MethodDelete, "/messages/"+source.MessageID.String(), f.member.token, map[string]any{"delete_for_all": true}, nil); err != nil {
		return err
	}
	replayed, err := f.d.interactionControl(f.member, run.ID, "input", canary+" VALUE=9", key, map[string]any{"source_message_id": source.MessageID})
	if err != nil {
		return err
	}
	if replayed.InputVersion != 2 {
		return errors.New("accepted input replay changed its durable order")
	}
	if _, err = f.input(f.owner, run.ID, "COMPLETE VALUE=2", nil); err != nil {
		return err
	}
	done, err := f.d.interactionWait(f.owner, run.ID, "succeeded")
	if err != nil {
		return err
	}
	if !strings.Contains(done.ResultText, "\"sum\": 12") {
		return fmt.Errorf("deleted supplement changed accepted work: %s", done.ResultText)
	}
	view, err := f.read(f.member, run.ID)
	if err != nil {
		return err
	}
	if len(view.InputSources) != 3 || view.InputSources[1].Content == nil || *view.InputSources[1].Content != canary+" VALUE=9" {
		return errors.New("deleted supplement disappeared from accepted input view")
	}
	return f.save("withdrawn_supplement_"+strings.ToLower(label), done, token)
}

func (f *compactionFixture) personalDeletion(address string) error {
	socket, err := f.d.p6Connect(address, f.member)
	if err != nil {
		return err
	}
	defer socket.close()
	token := "CP_PERSONAL_" + f.suffix
	if _, err = f.gate(token, "arm", ""); err != nil {
		return err
	}
	source, err := f.d.sendMessage(f.owner, f.conv, "CANARY_PERSONAL_HIDDEN", 0)
	if err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" VALUE=5", map[string]any{"message_ids": []entityID{source.MessageID}})
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.member, run.ID, "waiting_input"); err != nil {
		return err
	}
	if err = f.d.request(http.MethodDelete, "/messages/"+source.MessageID.String(), f.member.token, map[string]any{"delete_for_all": false}, nil); err != nil {
		return err
	}
	if err = f.d.inboxMessageAbsent("accepted-source", f.member, source.MessageID); err != nil {
		return err
	}
	if _, err = f.input(f.member, run.ID, "COMPLETE VALUE=7", nil); err != nil {
		return err
	}
	done, err := f.d.interactionWait(f.member, run.ID, "succeeded")
	if err != nil {
		return err
	}
	result, err := f.personalResult(done, "CANARY_PERSONAL_HIDDEN")
	if err != nil {
		return err
	}
	if _, err = f.d.p6Wait(socket, 0, "independent-result-visible", func(e p6Event) bool {
		return e.Type == "message.new" && e.Message.MessageID == result.MessageID && strings.Contains(e.Message.Content.Text, "CANARY_PERSONAL_HIDDEN")
	}); err != nil {
		return err
	}
	if err = f.independentViews(f.member, []entityID{source.MessageID}, []storedMessage{result}, "CANARY_PERSONAL_HIDDEN"); err != nil {
		return err
	}
	return f.save("personal_deletion_shared_complete", done, token)
}

// Every negative result check is fenced by a positive publication, indexed
// result, and visible message on the same real consumer stream.
func (f *compactionFixture) personalResult(run interactionRun, canary string) (storedMessage, error) {
	if run.MessageID == nil || !strings.Contains(run.ResultText, canary) {
		return storedMessage{}, errors.New("personal-derived: completed shared result did not actually contain the cited canary")
	}
	result, err := f.d.identityMessage(f.owner, *run.MessageID)
	if err != nil {
		return result, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if !strings.Contains(string(raw), canary) {
		return result, errors.New("personal-derived: owner published result lost cited body")
	}
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err = f.d.request(http.MethodGet, "/convs/"+f.conv.String()+"/messages?cursor=0&limit=100", f.owner.token, nil, &history); err != nil {
		return result, err
	}
	if !slices.ContainsFunc(history.Messages, func(m storedMessage) bool { return reflect.DeepEqual(m, result) }) {
		return result, errors.New("personal-derived: owner's history lost independently published shared result")
	}
	deadline := time.Now().Add(f.d.timeout)
	for time.Now().Before(deadline) {
		var search inboxSearchResult
		if err = f.d.request(http.MethodGet, "/messages/search?conversation_id="+f.conv.String()+"&keyword="+canary+"&page=1&page_size=100", f.owner.token, nil, &search); err != nil {
			return result, err
		}
		if slices.ContainsFunc(search.Messages, func(m storedMessage) bool { return m.MessageID == result.MessageID }) {
			return result, nil
		}
		time.Sleep(min(25*time.Millisecond, max(0, time.Until(deadline))))
	}
	return result, errors.New("personal-derived: published canary result never entered owner's search; absence would be vacuous")
}

// The original identity must stay hidden, but equal content in independent
// work remains readable. Content-wide canary bans would assert the old contract.
func (f *compactionFixture) independentViews(caller account, hidden []entityID, results []storedMessage, canary string) error {
	var history struct {
		Messages []storedMessage `json:"messages"`
	}
	if err := f.d.request(http.MethodGet, "/convs/"+f.conv.String()+"/messages?cursor=0&limit=100", caller.token, nil, &history); err != nil {
		return err
	}
	for _, id := range hidden {
		if err := f.d.inboxMessageAbsent("original-only", caller, id); err != nil {
			return err
		}
		if slices.ContainsFunc(history.Messages, func(m storedMessage) bool { return m.MessageID == id }) {
			return errors.New("original hidden message returned in history")
		}
	}
	var search inboxSearchResult
	if err := f.d.request(http.MethodGet, "/messages/search?conversation_id="+f.conv.String()+"&keyword="+canary+"&page=1&page_size=100", caller.token, nil, &search); err != nil {
		return err
	}
	for _, id := range hidden {
		if slices.ContainsFunc(search.Messages, func(m storedMessage) bool { return m.MessageID == id }) {
			return errors.New("search restored hidden original identity")
		}
	}
	for _, expected := range results {
		actual, err := f.d.identityMessage(caller, expected.MessageID)
		if err != nil {
			return err
		}
		if actual.Text.Text != expected.Text.Text || !strings.Contains(actual.Text.Text, canary) {
			return errors.New("independent result lost accepted content")
		}
		if !slices.ContainsFunc(history.Messages, func(m storedMessage) bool {
			return m.MessageID == expected.MessageID && m.Text.Text == expected.Text.Text
		}) {
			return errors.New("independent result missing in history")
		}
		if !slices.ContainsFunc(search.Messages, func(m storedMessage) bool { return m.MessageID == expected.MessageID }) {
			return errors.New("independent result missing in search")
		}
	}
	return nil
}

func (f *compactionFixture) personalDeletionPublished(address string) error {
	socket, err := f.d.p6Connect(address, f.member)
	if err != nil {
		return err
	}
	defer socket.close()
	token, canary := "CP_PUBLISHED_"+f.suffix, "CANARY_PERSONAL_PUBLISHED"
	if _, err = f.gate(token, "arm", ""); err != nil {
		return err
	}
	source, err := f.d.sendMessage(f.owner, f.conv, canary, 0)
	if err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" COMPLETE VALUE=5", map[string]any{"message_ids": []entityID{source.MessageID}})
	if err != nil {
		return err
	}
	run, err = f.d.interactionWait(f.owner, run.ID, "succeeded")
	if err != nil {
		return err
	}
	result, err := f.personalResult(run, canary)
	if err != nil {
		return err
	}
	child, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" COMPLETE VALUE=3", map[string]any{"parent_run_id": run.ID})
	if err != nil {
		return err
	}
	child, err = f.d.interactionWait(f.owner, child.ID, "succeeded")
	if err != nil {
		return err
	}
	childResult, err := f.personalResult(child, canary)
	if err != nil {
		return err
	}
	before, err := f.d.inboxRead("independent-before-delete", f.member, 0, 100)
	if err != nil {
		return err
	}
	cursor := socket.cursor()
	if err = f.d.request(http.MethodDelete, "/messages/"+source.MessageID.String(), f.member.token, map[string]any{"delete_for_all": false}, nil); err != nil {
		return err
	}
	if _, err = f.d.p6Wait(socket, cursor, "original-tombstone", func(e p6Event) bool {
		return e.Type == "message.deleted" && hasEntityID(e.MessageID, source.MessageID)
	}); err != nil {
		return err
	}
	continued, err := f.d.interactionStart(f.member, f.bot.ID, f.conv, token+" COMPLETE VALUE=7", map[string]any{"parent_run_id": child.ID})
	if err != nil {
		return err
	}
	continued, err = f.d.interactionWait(f.member, continued.ID, "succeeded")
	if err != nil {
		return err
	}
	continuedResult, err := f.personalResult(continued, canary)
	if err != nil {
		return err
	}
	if _, err = f.d.p6Wait(socket, cursor, "independent-child-visible", func(e p6Event) bool {
		return e.Type == "message.new" && e.Message.MessageID == continuedResult.MessageID && strings.Contains(e.Message.Content.Text, canary)
	}); err != nil {
		return err
	}
	socket.mu.Lock()
	for _, evt := range socket.events[cursor:] {
		if evt.Type == "message.deleted" && (hasEntityID(evt.MessageID, result.MessageID) || hasEntityID(evt.MessageID, childResult.MessageID)) {
			socket.mu.Unlock()
			return errors.New("source deletion cascaded result cache eviction")
		}
	}
	socket.mu.Unlock()
	results := []storedMessage{result, childResult, continuedResult}
	if err = f.independentViews(f.member, []entityID{source.MessageID}, results, canary); err != nil {
		return err
	}
	for _, completed := range []interactionRun{run, child, continued} {
		view, err := f.read(f.member, completed.ID)
		if err != nil {
			return err
		}
		if view.ResultText != completed.ResultText || view.Status != "succeeded" {
			return errors.New("source operation changed independent Run")
		}
	}
	// Current synchronization hydrates tombstones only for the original message.
	position, seenOriginal, seenResult := before.NextPosition, false, false
	if err = f.d.broadcastPoll("original tombstone and independent output sync", func(*driver) (bool, error) {
		page, err := f.d.inboxRead("independent-reconnect", f.member, position, 100)
		if err != nil {
			return false, err
		}
		for _, change := range page.Changes {
			if change.Message.MessageID == source.MessageID {
				return false, errors.New("sync restored original body")
			}
			if change.Kind == "message.deleted" && change.MessageID != nil {
				seenOriginal = seenOriginal || *change.MessageID == source.MessageID
				if *change.MessageID == result.MessageID || *change.MessageID == childResult.MessageID {
					return false, errors.New("sync hid independent result")
				}
			}
			seenResult = seenResult || change.Message.MessageID == continuedResult.MessageID && strings.Contains(change.Message.Text.Text, canary)
		}
		position = page.NextPosition
		return seenOriginal && seenResult && !page.HasMore, nil
	}); err != nil {
		return err
	}
	fresh, err := f.d.inboxLoginDevice(f.member, "independent-rebuild")
	if err != nil {
		return err
	}
	page, err := f.d.inboxRead("independent-rebuild", fresh, 0, 100)
	if err != nil {
		return err
	}
	if err = checkInboxRebuild("independent-rebuild", "new_device", page); err != nil {
		return err
	}
	found := false
	for _, snapshot := range page.Conversations {
		for _, m := range snapshot.Messages {
			if m.MessageID == source.MessageID {
				return errors.New("rebuild restored original message")
			}
			found = found || m.MessageID == continuedResult.MessageID && strings.Contains(m.Text.Text, canary)
		}
	}
	if !found {
		return errors.New("new-device rebuild omitted independent latest result")
	}
	// Deleting a result message changes that message, never the Run or its child.
	if err = f.d.request(http.MethodDelete, "/messages/"+result.MessageID.String(), f.member.token, map[string]any{"delete_for_all": false}, nil); err != nil {
		return err
	}
	if err = f.d.inboxMessageAbsent("result-only", f.member, result.MessageID); err != nil {
		return err
	}
	outputChild, err := f.d.interactionStart(f.member, f.bot.ID, f.conv, token+" COMPLETE VALUE=2", map[string]any{"parent_run_id": run.ID})
	if err != nil {
		return err
	}
	outputChild, err = f.d.interactionWait(f.member, outputChild.ID, "succeeded")
	if err != nil {
		return err
	}
	outputResult, err := f.personalResult(outputChild, canary)
	if err != nil {
		return err
	}
	if err = f.independentViews(f.member, []entityID{source.MessageID, result.MessageID}, []storedMessage{childResult, continuedResult, outputResult}, canary); err != nil {
		return err
	}
	// Global source removal still cannot remove work copies or independent results.
	if err = f.d.request(http.MethodPost, "/messages/"+source.MessageID.String()+"/recall", f.owner.token, map[string]any{}, nil); err != nil {
		return err
	}
	if err = f.d.request(http.MethodDelete, "/messages/"+source.MessageID.String(), f.owner.token, map[string]any{"delete_for_all": true}, nil); err != nil {
		return err
	}
	if err = f.independentViews(f.member, []entityID{source.MessageID, result.MessageID}, []storedMessage{childResult, continuedResult, outputResult}, canary); err != nil {
		return err
	}
	if err = f.save("personal_deletion_published_child", continued, token); err != nil {
		return err
	}
	return f.save("personal_deletion_published", run, token)
}

func (f *compactionFixture) boundaries(parent interactionRun, oldToken string) error {
	for _, operation := range []string{"clear_context", "close_interaction"} {
		closed, err := f.d.interactionControl(f.owner, parent.ID, operation, "", uuid.NewString(), nil)
		if err != nil {
			return err
		}
		if closed.InteractionStatus != "closed" || closed.Status != "succeeded" {
			return errors.New("context boundary erased completed work or remained open")
		}
		if err = f.d.runtimeReject(http.MethodPost, "/bots/"+f.bot.ID.String()+"/runs", f.member, map[string]any{"conv_id": f.conv, "submission_key": uuid.NewString(), "message": "closed continuation", "parent_run_id": parent.ID}, http.StatusConflict); err != nil {
			return err
		}
		token := "CP_BOUNDARY" + strings.ToUpper(strings.ReplaceAll(operation, "_", "")) + "_" + f.suffix
		if _, err = f.gate(token, "arm", ""); err != nil {
			return err
		}
		fresh, err := f.d.interactionStart(f.member, f.bot.ID, f.conv, token+" COMPLETE VALUE=3", nil)
		if err != nil {
			return err
		}
		fresh, err = f.d.interactionWait(f.member, fresh.ID, "succeeded")
		if err != nil {
			return err
		}
		if fresh.InteractionID == parent.InteractionID {
			return errors.New("clear/close reused old interaction")
		}
		rows, err := f.gate(token, "observe", "")
		if err != nil {
			return err
		}
		for _, row := range rows.Requests {
			if strings.Contains(string(row.Body["messages"]), oldToken) {
				return errors.New("closed summary leaked into fresh work")
			}
		}
		parent = fresh
	}
	return nil
}

func (f *compactionFixture) resourceRevocation() (result error) {
	token := "CP_RESOURCE_" + f.suffix
	if _, err := f.gate(token, "arm", "summary"); err != nil {
		return err
	}
	defer func() {
		if _, err := f.gate(token, "release", ""); err != nil {
			result = errors.Join(result, fmt.Errorf("compaction release %s: %w", token, err))
		}
	}()
	file, err := f.d.attachmentUpload(f.owner, "compaction-resource-"+f.suffix+".txt", "text/plain", []byte("CANARY_RESOURCE_REVOKED"), 3)
	if err != nil {
		return err
	}
	grant, err := f.d.resourceGrant(f.owner, f.bot.ID, f.conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+compactionPadding(5), map[string]any{"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{grant.ID}})
	if err != nil {
		return err
	}
	if err = f.save("held_resource_revocation", run, token); err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	held, _, _, err := f.driveSummary(run, token)
	if err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/resources", f.owner.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
		return err
	}
	if _, err = f.gate(token, "release", ""); err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "stopped"); err != nil {
		var api *apiError
		if !errors.As(err, &api) || api.status != http.StatusForbidden {
			return err
		}
	}
	if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.owner.token, map[string]any{"conv_id": f.conv, "submission_key": uuid.NewString(), "message": token + " forbidden continuation", "parent_run_id": run.ID}); err != nil {
		return err
	}
	if err = f.d.authorizationReject("/bot-authorizations/resources", f.owner.token, map[string]any{"op": "read", "run_id": run.ID, "grant_id": grant.ID}); err != nil {
		return err
	}
	if err = f.d.broadcastPoll("held revoked compaction response settled", func(*driver) (bool, error) {
		rows, err := f.gate(token, "observe", "")
		return len(rows.Requests) >= len(held.Requests) && rows.Requests[len(held.Requests)-1].State == "responded", err
	}); err != nil {
		return err
	}
	after, err := f.gate(token, "observe", "")
	if err != nil {
		return err
	}
	for _, row := range after.Requests[len(held.Requests):] {
		if strings.Contains(string(row.Body["messages"]), "CANARY_RESOURCE_REVOKED") {
			return errors.New("revoked resource leaked to subsequent request")
		}
	}
	return nil
}

func (f *compactionFixture) omittedMaterial() error {
	token := "CP_OMITTED_" + f.suffix
	if _, err := f.gate(token, "arm", ""); err != nil {
		return err
	}
	file, err := f.d.attachmentUpload(f.owner, "compaction-large-"+f.suffix+".txt", "text/plain", []byte("CANARY_OVERSIZED_UNANALYZED "+strings.Repeat("large external material ", 2000)), 3)
	if err != nil {
		return err
	}
	grant, err := f.d.resourceGrant(f.owner, f.bot.ID, f.conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" COMPLETE VALUE=5", map[string]any{"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{grant.ID}})
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "succeeded"); err != nil {
		return err
	}
	public, err := f.read(f.owner, run.ID)
	if err != nil {
		return err
	}
	if public.ContextNotice == "" {
		return errors.New("oversized omitted material lacked public not-analyzed notice")
	}
	rows, err := f.gate(token, "observe", "")
	if err != nil {
		return err
	}
	if len(rows.Requests) == 0 {
		return errors.New("omission never exercised actual model boundary")
	}
	for _, row := range rows.Requests {
		if strings.Contains(string(row.Body["messages"]), "CANARY_OVERSIZED_UNANALYZED") {
			return errors.New("oversized material silently included despite omission")
		}
	}
	if err = f.save("oversized_material_notice", run, token); err != nil {
		return err
	}
	largeToken := "CP_LARGE_" + f.suffix
	if _, err = f.gate(largeToken, "arm", ""); err != nil {
		return err
	}
	tooLarge, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, largeToken+" COMPLETE VALUE=99 "+strings.Repeat("accepted precise input ", 2000), nil)
	if err != nil {
		return err
	}
	failed, err := f.wait(f.owner, tooLarge.ID, func(r compactionRun) bool { return r.Status == "failed" })
	if err != nil {
		return err
	}
	if failed.Error == "" || failed.MessageID != nil || failed.OutputStatus != "none" {
		return errors.New("oversized current accepted input silently omitted or published success")
	}
	actual, err := f.gate(largeToken, "observe", "")
	if err != nil {
		return err
	}
	if len(actual.Requests) != 0 {
		return errors.New("oversized accepted current input was truncated into model request")
	}
	return f.save("oversized_current_rejected", tooLarge, largeToken)
}

func (f *compactionFixture) browserFixtures() error {
	token := "CP_CAS_" + f.suffix
	if _, err := f.gate(token, "arm", ""); err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" VALUE=1", nil)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	if _, err = f.input(f.member, run.ID, "CAS_SECOND VALUE=2", nil); err != nil {
		return err
	}
	run, err = f.d.interactionWait(f.owner, run.ID, "waiting_input")
	if err != nil {
		return err
	}
	if err = f.save("cas_waiting", run, token); err != nil {
		return err
	}
	for _, label := range []string{"MODIFIED", "WITHDRAWN"} {
		token = "CP_BROWSER" + label + "_" + f.suffix
		if _, err = f.gate(token, "arm", ""); err != nil {
			return err
		}
		source, err := f.d.sendMessage(f.owner, f.conv, "CANARY_BROWSER_"+label, 0)
		if err != nil {
			return err
		}
		run, err = f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" VALUE=3", map[string]any{"message_ids": []entityID{source.MessageID}})
		if err != nil {
			return err
		}
		if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
			return err
		}
		if label == "MODIFIED" {
			if err = f.d.request(http.MethodPut, "/messages/"+source.MessageID.String(), f.owner.token, map[string]any{"text": "CANARY_BROWSER_CURRENT_NOT_ACCEPTED"}, nil); err != nil {
				return err
			}
		} else {
			if err = f.d.request(http.MethodPost, "/messages/"+source.MessageID.String()+"/recall", f.owner.token, map[string]any{}, nil); err != nil {
				return err
			}
		}
		if err = f.save("browser_"+strings.ToLower(label), run, token); err != nil {
			return err
		}
	}
	return nil
}

func (f *compactionFixture) historyAuthorization() error {
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err := f.d.request(http.MethodPost, "/convs", f.owner.token, map[string]any{"type": "group", "group_name": "history-authority-" + f.suffix, "member_ids": []entityID{f.member.id}}, &conv); err != nil {
		return err
	}
	prior := f.conv
	f.conv = conv.ID
	defer func() { f.conv = prior }()
	if err := f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.owner.token, map[string]any{"bot_id": f.bot.ID}, nil); err != nil {
		return err
	}
	binding, err := f.d.identityBinding(f.owner, f.conv, f.bot.ID)
	if err != nil {
		return err
	}
	source, err := f.d.sendMessage(f.owner, f.conv, "CANARY_HISTORY_ACCEPTED", 0)
	if err != nil {
		return err
	}
	grant, err := f.d.identityGrant(f.member, f.conv, f.bot.ID, binding.BindingID, source.Seq, source.Seq)
	if err != nil {
		return err
	}
	token := "CP_HISTORY_" + f.suffix
	if _, err = f.gate(token, "arm", ""); err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+" VALUE=5", map[string]any{"history_grant_id": grant})
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	if err = f.d.request(http.MethodDelete, "/messages/"+source.MessageID.String(), f.owner.token, map[string]any{"delete_for_all": true}, nil); err != nil {
		return err
	}
	if _, err = f.input(f.owner, run.ID, "COMPLETE VALUE=7", nil); err != nil {
		return err
	}
	done, err := f.d.interactionWait(f.owner, run.ID, "succeeded")
	if err != nil {
		return err
	}
	if !strings.Contains(done.ResultText, "CANARY_HISTORY_ACCEPTED") || !strings.Contains(done.ResultText, "\"sum\": 12") {
		return errors.New("deleted original invalidated accepted historical range")
	}
	// New history reads cannot recover a globally removed original message.
	freshToken := "CP_HISTORYFRESH_" + f.suffix
	if _, err = f.gate(freshToken, "arm", ""); err != nil {
		return err
	}
	fresh, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, freshToken+" COMPLETE VALUE=3", map[string]any{"history_grant_id": grant})
	if err != nil {
		return err
	}
	fresh, err = f.d.interactionWait(f.owner, fresh.ID, "succeeded")
	if err != nil {
		return err
	}
	if strings.Contains(fresh.ResultText, "CANARY_HISTORY_ACCEPTED") {
		return errors.New("new historical read resurrected removed original")
	}
	// The author leaving is actual history-grant revocation, unlike IM deletion.
	if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/kick", f.owner.token, map[string]any{"user_ids": []entityID{f.member.id}}, nil); err != nil {
		return err
	}
	if err = f.d.authorizationReject("/bots/"+f.bot.ID.String()+"/runs", f.owner.token, map[string]any{"conv_id": f.conv, "submission_key": uuid.NewString(), "message": token + " COMPLETE VALUE=2", "parent_run_id": run.ID}); err != nil {
		return err
	}
	revoked, err := f.read(f.owner, run.ID)
	if err == nil {
		raw, err := json.Marshal(revoked)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "CANARY_HISTORY_ACCEPTED") {
			return errors.New("revoked historical content remained disclosed")
		}
	} else {
		var denied *apiError
		if !errors.As(err, &denied) || denied.status != http.StatusForbidden {
			return err
		}
	}
	return nil
}

func (f *compactionFixture) summaryExplicitStop() (result error) {
	token := "CP_EXPLICITSTOP_" + f.suffix
	if _, err := f.gate(token, "arm", "summary"); err != nil {
		return err
	}
	defer func() {
		if _, err := f.gate(token, "release", ""); err != nil {
			result = errors.Join(result, fmt.Errorf("compaction release %s: %w", token, err))
		}
	}()
	run, err := f.d.interactionStart(f.owner, f.bot.ID, f.conv, token+compactionPadding(5), nil)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionWait(f.owner, run.ID, "waiting_input"); err != nil {
		return err
	}
	held, _, _, err := f.driveSummary(run, token)
	if err != nil {
		return err
	}
	if _, err = f.d.interactionControl(f.owner, run.ID, "stop", "", uuid.NewString(), nil); err != nil {
		return err
	}
	if _, err = f.gate(token, "release", ""); err != nil {
		return err
	}
	stopped, err := f.d.interactionWait(f.owner, run.ID, "stopped")
	if err != nil {
		return err
	}
	if stopped.MessageID != nil || stopped.ResultText != "" {
		return errors.New("explicit stop during summary published a result")
	}
	if err = f.d.broadcastPoll("stopped summary settled at external endpoint", func(*driver) (bool, error) {
		observed, err := f.gate(token, "observe", "")
		return len(observed.Requests) >= len(held.Requests) && observed.Requests[len(held.Requests)-1].State == "responded", err
	}); err != nil {
		return err
	}
	observed, err := f.gate(token, "observe", "")
	if err != nil {
		return err
	}
	for _, request := range observed.Requests[len(held.Requests):] {
		if request.Kind == "work" {
			return errors.New("stale stopped summary advanced work")
		}
	}
	return f.save("summary_explicit_stop", stopped, token)
}
