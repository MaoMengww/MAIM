package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The public REST/WS journey observes actual provider requests, never database rows.
type interactionRun struct {
	publicRun
	ParentRunID       *entityID `json:"parent_run_id"`
	InteractionStatus string    `json:"interaction_status"`
	WaitingReason     string    `json:"waiting_reason"`
}

type interactionInput struct {
	Version         int64     `json:"version"`
	RequesterID     entityID  `json:"requester_id"`
	SourceMessageID *entityID `json:"source_message_id"`
	Content         string    `json:"content"`
}

type interactionResult struct {
	Sum          int64      `json:"sum"`
	Contributors []entityID `json:"contributors"`
	Materials    []struct {
		Kind        string   `json:"kind"`
		Content     string   `json:"content"`
		SenderType  string   `json:"sender_type"`
		SenderID    entityID `json:"sender_id"`
		MessageID   entityID `json:"message_id"`
		FileID      entityID `json:"file_id"`
		ParentRunID entityID `json:"parent_run_id"`
		RequesterID entityID `json:"requester_id"`
	} `json:"materials"`
}

func (d *driver) interactionStart(caller account, bot, conv entityID, text string, extra map[string]any) (interactionRun, error) {
	body := map[string]any{"conv_id": conv, "submission_key": uuid.NewString(), "message": text}
	for key, value := range extra {
		body[key] = value
	}
	var run interactionRun
	err := d.request(http.MethodPost, "/bots/"+bot.String()+"/runs", caller.token, body, &run)
	return run, err
}

func (d *driver) interactionControl(caller account, id entityID, operation, text, key string, extra map[string]any) (interactionRun, error) {
	body := map[string]any{"operation": operation, "submission_key": key}
	if text != "" {
		body["message"] = text
	}
	for key, value := range extra {
		body[key] = value
	}
	var run interactionRun
	err := d.request(http.MethodPost, "/runs/"+id.String()+"/control", caller.token, body, &run)
	return run, err
}

func (d *driver) interactionWait(caller account, id entityID, status string) (interactionRun, error) {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		var run interactionRun
		if err := d.request(http.MethodGet, "/runs/"+id.String(), caller.token, nil, &run); err != nil {
			return run, err
		}
		if run.Status == status && (status != "succeeded" || run.OutputStatus == "committed") {
			return run, nil
		}
		if run.Status == "failed" || run.Status == "stopped" || run.Status == "succeeded" && status != "succeeded" {
			return run, fmt.Errorf("interaction: expected %s, got %s: %s", status, run.Status, run.Error)
		}
		time.Sleep(30 * time.Millisecond)
	}
	return interactionRun{}, fmt.Errorf("interaction: %s not observed before deadline", status)
}

func interactionInputs(request map[string]json.RawMessage) ([]interactionInput, []map[string]json.RawMessage, error) {
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(request["messages"], &messages); err != nil {
		return nil, nil, err
	}
	var inputs []interactionInput
	for _, message := range messages {
		var role, content string
		if err := json.Unmarshal(message["role"], &role); err != nil {
			return nil, nil, err
		}
		if role != "user" {
			continue
		}
		if err := json.Unmarshal(message["content"], &content); err != nil {
			return nil, nil, err
		}
		if raw, ok := strings.CutPrefix(content, "Accepted work input (not authorization):\n"); ok {
			var input interactionInput
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				return nil, nil, err
			}
			inputs = append(inputs, input)
		}
	}
	return inputs, messages, nil
}

func (d *driver) interactionRequests(token string) ([]map[string]json.RawMessage, error) {
	observed, err := d.observeProvider()
	if err != nil {
		return nil, err
	}
	var requests []map[string]json.RawMessage
	for _, request := range observed.ChatRequests {
		var model string
		if err := json.Unmarshal(request["model"], &model); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(model, "fixture-interaction-") {
			continue
		}
		inputs, _, err := interactionInputs(request)
		if err != nil {
			return nil, err
		}
		if len(inputs) != 0 && strings.Contains(inputs[0].Content, token) {
			requests = append(requests, request)
		}
	}
	return requests, nil
}

func (d *driver) interactionGate(token, action string) error {
	var response struct {
		OK bool `json:"ok"`
	}
	if err := d.executorProvider("/interaction/"+action, map[string]string{"token": token}, &response); err != nil {
		return err
	}
	if !response.OK {
		return errors.New("interaction: provider gate rejected")
	}
	return nil
}

func (d *driver) interactionHeldRequest(token string) (map[string]json.RawMessage, error) {
	deadline := time.Now().Add(d.timeout)
	for time.Now().Before(deadline) {
		var response struct {
			OK bool `json:"ok"`
		}
		if err := d.executorProvider("/interaction/held", map[string]string{"token": token}, &response); err != nil {
			return nil, err
		}
		if response.OK {
			requests, err := d.interactionRequests(token)
			if err != nil {
				return nil, err
			}
			if len(requests) < 2 {
				return nil, errors.New("interaction: held call was not actually observed")
			}
			return requests[len(requests)-1], nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, errors.New("interaction: resumed model call never reached gate")
}

func interactionDecodedResult(run interactionRun) (interactionResult, error) {
	var result interactionResult
	if run.Status != "succeeded" || run.OutputStatus != "committed" || run.MessageID == nil {
		return result, errors.New("interaction: no authoritative published result")
	}
	if err := json.Unmarshal([]byte(run.ResultText), &result); err != nil {
		return result, fmt.Errorf("interaction: provider did not compute actual materials: %w", err)
	}
	return result, nil
}

func (d *driver) botInteractions(address, secondAddress string) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	owner, err := d.register("interaction_owner", suffix)
	if err != nil {
		return err
	}
	member, err := d.register("interaction_member", suffix)
	if err != nil {
		return err
	}
	outsider, err := d.register("interaction_outsider", suffix)
	if err != nil {
		return err
	}
	modelID, err := d.fixtureModel(owner, "chat", "fixture-interaction-"+suffix)
	if err != nil {
		return err
	}
	var conv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "group", "group_name": "interactions-" + suffix, "member_ids": []entityID{member.id}}, &conv); err != nil {
		return err
	}
	var bot botView
	if err = d.request(http.MethodPost, "/bots", owner.token, map[string]any{"name": "interaction-" + suffix, "type": "self_deployed", "model_id": modelID, "system_prompt": "Compute VALUE amounts only from accepted inputs and explicitly cited materials. Ask for three supplements when INTERACTION_WAIT is requested.", "response_triggers": []string{"mention"}, "streaming_enabled": false, "enable_knowledge": false}, &bot); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+conv.ID.String()+"/bots", owner.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
		return err
	}
	fmt.Printf("Interaction browser accounts: owner=%s password=%s; member=%s password=%s; group=%s bot=%s\n", owner.username, owner.password, member.username, member.password, conv.ID, bot.ID)
	socket, err := d.p6Connect(secondAddress, member)
	if err != nil {
		return err
	}
	defer socket.close()
	if _, err = d.sendMessage(owner, conv.ID, "UNRELATED_NEVER_INCLUDE VALUE=999 "+suffix, 0); err != nil {
		return err
	}
	// First behavior: a real model tool request pauses work, not a successful answer.
	token := "INTERACTION_WAIT_" + suffix
	waiting, err := d.interactionStart(owner, bot.ID, conv.ID, token+" VALUE=5", nil)
	if err != nil {
		return err
	}
	waiting, err = d.interactionWait(member, waiting.ID, "waiting_input")
	if err != nil {
		return err
	}
	if waiting.WaitingReason == "" || waiting.OutputStatus != "none" || waiting.MessageID != nil || waiting.InteractionStatus != "active" || !waiting.ExecutorCapabilities.SupportsInput || !waiting.ExecutorCapabilities.SupportsStop || !waiting.ExecutorCapabilities.SupportsResume {
		return errors.New("interaction.wait: ask_for_input missing reason, published success, or wrong builtin capabilities")
	}
	if _, err = d.p6Wait(socket, 0, "interaction-wait", func(e p6Event) bool {
		return e.Type == "bot.run.updated" && e.Run.ID == waiting.ID && e.Run.Status == "waiting_input"
	}); err != nil {
		return err
	}
	for _, operation := range []string{"close_interaction", "clear_context"} {
		if err = d.runtimeReject(http.MethodPost, "/runs/"+waiting.ID.String()+"/control", owner, map[string]any{"operation": operation, "submission_key": uuid.NewString()}, http.StatusConflict); err != nil {
			return err
		}
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+waiting.ID.String()+"/control", outsider, map[string]any{"operation": "input", "message": "VALUE=1000", "submission_key": uuid.NewString()}, http.StatusForbidden); err != nil {
		return err
	}
	// The same Bot must run unrelated work while the first Run is waiting.
	independent, err := d.interactionStart(member, bot.ID, conv.ID, "INTERACTION_INDEPENDENT_"+suffix+" VALUE=17", nil)
	if err != nil {
		return err
	}
	if independent.InteractionID == waiting.InteractionID {
		return errors.New("interaction.parallel: unrelated work shared interaction")
	}
	independent, err = d.interactionWait(member, independent.ID, "succeeded")
	if err != nil {
		return err
	}
	independentResult, err := interactionDecodedResult(independent)
	if err != nil {
		return err
	}
	if independentResult.Sum != 17 || len(independentResult.Materials) != 0 {
		return errors.New("interaction.parallel: waiting Run/recent history contaminated independent Run")
	}
	// Concurrent receipts are serialized by authoritative input version, not CAS lost.
	if err = d.interactionGate(token, "arm"); err != nil {
		return err
	}
	defer d.interactionGate(token, "release")
	callers := []account{owner, member}
	texts := []string{"FIRST_SUPPLEMENT_" + suffix + " VALUE=7", "SECOND_SUPPLEMENT_" + suffix + " VALUE=11"}
	keys := []string{uuid.NewString(), uuid.NewString()}
	var receipts [2]interactionRun
	var failures [2]error
	var wg sync.WaitGroup
	for i := range receipts {
		wg.Go(func() {
			receipts[i], failures[i] = d.interactionControl(callers[i], waiting.ID, "input", texts[i], keys[i], nil)
		})
	}
	wg.Wait()
	for i, failure := range failures {
		if failure != nil {
			return fmt.Errorf("interaction.concurrent participant %d: %w", i, failure)
		}
	}
	versions := []int64{receipts[0].InputVersion, receipts[1].InputVersion}
	slices.Sort(versions)
	if !slices.Equal(versions, []int64{waiting.InputVersion + 1, waiting.InputVersion + 2}) {
		return errors.New("interaction.concurrent: input overwritten or noncontiguous version")
	}
	for i := range receipts {
		replay, err := d.interactionControl(callers[i], waiting.ID, "input", texts[i], keys[i], nil)
		if err != nil {
			return err
		}
		if replay.Version != receipts[i].Version || replay.InputVersion != receipts[i].InputVersion {
			return errors.New("interaction.receipt: replay replaced original acceptance")
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+waiting.ID.String()+"/control", callers[i], map[string]any{"operation": "input", "message": texts[i] + " different", "submission_key": keys[i]}, http.StatusConflict); err != nil {
			return err
		}
	}
	held, err := d.interactionHeldRequest(token)
	if err != nil {
		return err
	}
	thirdText := "DURING_MODEL_CALL_" + suffix + " VALUE=13"
	third, err := d.interactionControl(member, waiting.ID, "input", thirdText, uuid.NewString(), nil)
	if err != nil {
		return err
	}
	if third.InputVersion != waiting.InputVersion+3 {
		return errors.New("interaction.inflight: third input not ordered")
	}
	if strings.Contains(string(held["messages"]), thirdText) {
		return errors.New("interaction.snapshot: future input present in immutable in-flight request")
	}
	if err = d.interactionGate(token, "release"); err != nil {
		return err
	}
	done, err := d.interactionWait(member, waiting.ID, "succeeded")
	if err != nil {
		return err
	}
	computed, err := interactionDecodedResult(done)
	if err != nil {
		return err
	}
	if computed.Sum != 36 || !slices.Contains(computed.Contributors, owner.id) || !slices.Contains(computed.Contributors, member.id) {
		return errors.New("interaction.result: concurrent or in-flight accepted material was lost")
	}
	requests, err := d.interactionRequests(token)
	if err != nil {
		return err
	}
	var previous []map[string]json.RawMessage
	for _, request := range requests {
		inputs, messages, err := interactionInputs(request)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(request["tools"], requests[0]["tools"]) || !reflect.DeepEqual(request["model"], requests[0]["model"]) {
			return errors.New("interaction.prefix: fixed model/tools prefix changed")
		}
		if len(previous) > len(messages) || !reflect.DeepEqual(previous, messages[:len(previous)]) && previous != nil {
			return errors.New("interaction.prefix: existing model messages rewritten/reordered")
		}
		if strings.Contains(string(request["messages"]), "UNRELATED_NEVER_INCLUDE") || strings.Contains(string(request["messages"]), "INTERACTION_INDEPENDENT_") {
			return errors.New("interaction.prefix: parallel or recent messages leaked")
		}
		for i, input := range inputs {
			if input.Version != int64(i+1) {
				return errors.New("interaction.trace: input versions duplicated or reordered")
			}
		}
		previous = messages
	}
	lastInputs, _, err := interactionInputs(requests[len(requests)-1])
	if err != nil {
		return err
	}
	if len(lastInputs) != 4 || lastInputs[3].Content != thirdText || lastInputs[3].RequesterID != member.id {
		return errors.New("interaction.trace: accepted identity/material missing from later call")
	}
	if err = d.interactionContinuation(owner, member, bot, conv.ID, done, independent, suffix); err != nil {
		return err
	}
	if err = d.interactionMaterials(owner, member, bot, conv.ID, suffix, address); err != nil {
		return err
	}
	if err = d.interactionExecutors(owner, member, conv.ID, suffix, address); err != nil {
		return err
	}
	return nil
}

func (d *driver) interactionContinuation(owner, member account, bot botView, conv entityID, parent, independent interactionRun, suffix string) error {
	path := "/runs/" + parent.ID.String() + "/control"
	if err := d.runtimeReject(http.MethodPost, path, member, map[string]any{"operation": "input", "message": "VALUE=200", "submission_key": uuid.NewString()}, http.StatusConflict); err != nil {
		return err
	}
	before, err := d.conversationRuns(member, conv)
	if err != nil {
		return err
	}
	// A reply to an already published execution card must not silently open a Run.
	var reply sentMessage
	if err = d.request(http.MethodPost, "/messages/send", member.token, map[string]any{"conversation_id": conv, "client_msg_id": uuid.NewString(), "reply_to_msg_id": *parent.MessageID, "content": map[string]any{"text": "TERMINAL_CARD_REPLY_" + suffix + " VALUE=200", "mentions": []entityID{bot.ID}}}, &reply); err != nil {
		return err
	}
	// Kafka is asynchronous: require the observation window, not one immediate list.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runs, err := d.conversationRuns(member, conv)
		if err != nil {
			return err
		}
		if len(runs) != len(before) {
			return errors.New("interaction.card-reply: terminal card reply silently created another Run")
		}
		time.Sleep(40 * time.Millisecond)
	}
	childToken := "INTERACTION_CHILD_" + suffix
	child, err := d.interactionStart(member, bot.ID, conv, childToken+" VALUE=19", map[string]any{"parent_run_id": parent.ID})
	if err != nil {
		return err
	}
	if child.ID == parent.ID || child.InteractionID != parent.InteractionID || !hasEntityID(child.ParentRunID, parent.ID) {
		return errors.New("interaction.continue: parent did not create a distinct related Run")
	}
	child, err = d.interactionWait(member, child.ID, "succeeded")
	if err != nil {
		return err
	}
	result, err := interactionDecodedResult(child)
	if err != nil {
		return err
	}
	if result.Sum != 55 {
		return fmt.Errorf("interaction.continue: expected authorized input snapshot plus child amount 55, got %d", result.Sum)
	}
	hasResult := false
	for _, material := range result.Materials {
		if material.ParentRunID == parent.ID && strings.Contains(material.Content, `"sum":36`) {
			hasResult = true
		}
	}
	if !hasResult {
		return errors.New("interaction.continue: parent result provenance/content missing")
	}
	requests, err := d.interactionRequests(childToken)
	if err != nil {
		return err
	}
	inputs, messages, err := interactionInputs(requests[0])
	if err != nil {
		return err
	}
	if len(inputs) != 1 || inputs[0].Content != childToken+" VALUE=19" {
		return errors.New("interaction.continue: shared parent trajectory rather than independent snapshot")
	}
	for _, message := range messages {
		var role string
		if err := json.Unmarshal(message["role"], &role); err != nil {
			return err
		}
		if role == "assistant" || role == "tool" {
			return errors.New("interaction.continue: parent tool/assistant trace leaked into child branch")
		}
	}
	for _, closed := range []struct {
		run       interactionRun
		operation string
	}{{parent, "close_interaction"}, {independent, "clear_context"}} {
		closedRun, err := d.interactionControl(owner, closed.run.ID, closed.operation, "", uuid.NewString(), nil)
		if err != nil {
			return err
		}
		if closedRun.InteractionStatus != "closed" || closedRun.Status != "succeeded" || closedRun.MessageID == nil || *closedRun.MessageID != *closed.run.MessageID {
			return errors.New("interaction.clear: closed state missing or execution fact erased")
		}
		if err = d.runtimeReject(http.MethodPost, "/bots/"+bot.ID.String()+"/runs", member, map[string]any{"conv_id": conv, "submission_key": uuid.NewString(), "message": "INTERACTION_CLOSED VALUE=1", "parent_run_id": closed.run.ID}, http.StatusConflict); err != nil {
			return err
		}
		stored, err := d.identityMessage(member, *closed.run.MessageID)
		if err != nil {
			return err
		}
		if stored.Text.Text != closed.run.ResultText {
			return errors.New("interaction.clear: historical result message was altered")
		}
	}
	// Closing any Run closes the relation, including descendants already created.
	if err = d.runtimeReject(http.MethodPost, "/bots/"+bot.ID.String()+"/runs", member, map[string]any{"conv_id": conv, "submission_key": uuid.NewString(), "message": "INTERACTION_CLOSED_CHILD VALUE=1", "parent_run_id": child.ID}, http.StatusConflict); err != nil {
		return err
	}
	fresh, err := d.interactionStart(member, bot.ID, conv, "INTERACTION_FRESH_"+suffix+" VALUE=23", nil)
	if err != nil {
		return err
	}
	if fresh.InteractionID == parent.InteractionID || fresh.InteractionID == independent.InteractionID || fresh.ParentRunID != nil {
		return errors.New("interaction.clear: fresh work reused closed context")
	}
	fresh, err = d.interactionWait(member, fresh.ID, "succeeded")
	if err != nil {
		return err
	}
	freshResult, err := interactionDecodedResult(fresh)
	if err != nil {
		return err
	}
	if freshResult.Sum != 23 || len(freshResult.Materials) != 0 {
		return errors.New("interaction.clear: old authorized snapshot inherited implicitly")
	}
	return nil
}

func (d *driver) interactionMaterials(owner, member account, bot botView, conv entityID, suffix, address string) error {
	binding, err := d.identityBinding(owner, conv, bot.ID)
	if err != nil {
		return err
	}
	selected, err := d.sendMessage(owner, conv, "EXPLICIT_MESSAGE_"+suffix+" VALUE=29", 0)
	if err != nil {
		return err
	}
	history, err := d.sendMessage(member, conv, "EXPLICIT_HISTORY_"+suffix+" VALUE=31", selected.Seq)
	if err != nil {
		return err
	}
	grant, err := d.identityGrant(owner, conv, bot.ID, binding.BindingID, history.Seq, history.Seq)
	if err != nil {
		return err
	}
	file, err := d.attachmentUpload(owner, "interaction-"+suffix+".txt", "text/plain", []byte("EXPLICIT_FILE_"+suffix+" VALUE=37"), 3)
	if err != nil {
		return err
	}
	fileGrant, err := d.resourceGrant(owner, bot.ID, conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	private, err := d.attachmentUpload(owner, "interaction-private-"+suffix+".txt", "text/plain", []byte("PRIVATE_FILE_NEVER_MODEL VALUE=999"), 1)
	if err != nil {
		return err
	}
	// Other Bot content comes from a genuine authenticated executor result.
	var other botView
	if err = d.request(http.MethodPost, "/bots", owner.token, map[string]any{"name": "interaction-source-" + suffix, "type": "third_party", "sub_type": "ws", "conn_mode": "ws", "response_triggers": []string{"mention"}, "capabilities": executorCapabilities}, &other); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", owner.token, map[string]any{"bot_id": other.ID}, nil); err != nil {
		return err
	}
	x, err := d.websocketExecutor(owner, other.ID, address, "interaction-source-"+suffix)
	if err != nil {
		return err
	}
	defer x.conn.Close()
	otherRun, err := d.startRun(member, other.ID, conv, "produce attributed material", uuid.NewString(), nil)
	if err != nil {
		return err
	}
	request, err := x.request(otherRun.ID)
	if err != nil {
		return err
	}
	otherText := "OTHER_BOT_MATERIAL_" + suffix + " VALUE=41"
	if _, err = x.complete(request, "text", map[string]any{"text": otherText}, otherText); err != nil {
		return err
	}
	otherRun, err = d.publishedRun(member, otherRun.ID)
	if err != nil {
		return err
	}
	materialToken := "INTERACTION_MATERIALS_" + suffix
	materialRun, err := d.interactionStart(member, bot.ID, conv, materialToken+" VALUE=43", map[string]any{"message_ids": []entityID{selected.MessageID, *otherRun.MessageID}, "history_grant_id": grant, "file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{fileGrant.ID}})
	if err != nil {
		return err
	}
	materialRun, err = d.interactionWait(member, materialRun.ID, "succeeded")
	if err != nil {
		return err
	}
	result, err := interactionDecodedResult(materialRun)
	if err != nil {
		return err
	}
	if result.Sum != 181 {
		return fmt.Errorf("interaction.material: expected actual selected message/history/file/other Bot sum 181, got %d", result.Sum)
	}
	foundMessage, foundHistory, foundFile, foundOther := false, false, false, false
	for _, source := range result.Materials {
		if source.MessageID == selected.MessageID && source.SenderType == "user" && source.SenderID == owner.id && source.Content == selected.Content.Text {
			foundMessage = true
		}
		if source.MessageID == history.MessageID && source.SenderType == "user" && source.SenderID == member.id && source.Content == history.Content.Text {
			foundHistory = true
		}
		if source.FileID == file.info.FileID && source.Kind == "file" && source.Content == string(file.data) && source.RequesterID == member.id && source.SenderType == "user" {
			foundFile = true
		}
		if source.MessageID == *otherRun.MessageID && source.SenderType == "bot" && source.SenderID == other.ID && source.Content == otherText {
			foundOther = true
		}
	}
	if !foundMessage || !foundHistory || !foundFile || !foundOther {
		return errors.New("interaction.material: source identity/type/content was flattened or omitted")
	}
	requests, err := d.interactionRequests(materialToken)
	if err != nil {
		return err
	}
	for _, actual := range requests {
		_, messages, err := interactionInputs(actual)
		if err != nil {
			return err
		}
		for _, message := range messages {
			if strings.Contains(string(message["content"]), otherText) {
				var role string
				if err = json.Unmarshal(message["role"], &role); err != nil {
					return err
				}
				if role != "user" {
					return errors.New("interaction.material: other Bot falsely represented as this Bot assistant/system")
				}
			}
		}
	}
	var otherConv struct {
		ID entityID `json:"conversation_id"`
	}
	if err = d.request(http.MethodPost, "/convs", owner.token, map[string]any{"type": "group", "group_name": "interaction-other-" + suffix, "member_ids": []entityID{member.id}}, &otherConv); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+otherConv.ID.String()+"/bots", owner.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
		return err
	}
	foreign, err := d.sendMessage(owner, otherConv.ID, "CROSS_GROUP_NEVER_MODEL VALUE=999", 0)
	if err != nil {
		return err
	}
	hidden, err := d.sendMessage(owner, conv, "PERSONALLY_HIDDEN_NEVER_MODEL VALUE=999", 0)
	if err != nil {
		return err
	}
	if err = d.request(http.MethodDelete, "/messages/"+hidden.MessageID.String(), member.token, map[string]bool{"delete_for_all": false}, nil); err != nil {
		return err
	}
	for _, extra := range []map[string]any{{"message_ids": []entityID{foreign.MessageID}}, {"message_ids": []entityID{hidden.MessageID}}, {"file_ids": []entityID{private.info.FileID}}, {"conv_id": otherConv.ID, "history_grant_id": grant}, {"conv_id": otherConv.ID, "parent_run_id": materialRun.ID}} {
		body := map[string]any{"conv_id": conv, "submission_key": uuid.NewString(), "message": "INTERACTION_DENIED_" + suffix + " VALUE=1"}
		for key, value := range extra {
			body[key] = value
		}
		if err = d.runtimeReject(http.MethodPost, "/bots/"+bot.ID.String()+"/runs", member, body, http.StatusForbidden); err != nil {
			return err
		}
	}
	if err = d.runtimeReject(http.MethodPost, "/bots/"+bot.ID.String()+"/runs", owner, map[string]any{"conv_id": conv, "submission_key": uuid.NewString(), "message": "INTERACTION_DENIED_" + suffix + " VALUE=1", "file_ids": []entityID{private.info.FileID}}, http.StatusForbidden); err != nil {
		return err
	}
	// Accepted supplement references must undergo the same authorization checks.
	waiting, err := d.interactionStart(member, bot.ID, conv, "INTERACTION_WAIT_MATERIAL_"+suffix+" VALUE=1", map[string]any{"resource_grant_ids": []entityID{fileGrant.ID}})
	if err != nil {
		return err
	}
	waiting, err = d.interactionWait(member, waiting.ID, "waiting_input")
	if err != nil {
		return err
	}
	for _, extra := range []map[string]any{{"message_ids": []entityID{foreign.MessageID}}, {"message_ids": []entityID{hidden.MessageID}}, {"file_ids": []entityID{private.info.FileID}}} {
		body := map[string]any{"operation": "input", "submission_key": uuid.NewString(), "message": "INTERACTION_DENIED_SUPPLEMENT VALUE=999"}
		for key, value := range extra {
			body[key] = value
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+waiting.ID.String()+"/control", member, body, http.StatusForbidden); err != nil {
			return err
		}
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+waiting.ID.String()+"/control", owner, map[string]any{"operation": "input", "submission_key": uuid.NewString(), "message": "PRIVATE_OWNER_DOES_NOT_AUTHORIZE_SHARING VALUE=999", "file_ids": []entityID{private.info.FileID}}, http.StatusForbidden); err != nil {
		return err
	}
	materialWaitToken := "INTERACTION_WAIT_MATERIAL_" + suffix
	if err = d.interactionGate(materialWaitToken, "arm"); err != nil {
		return err
	}
	defer d.interactionGate(materialWaitToken, "release")
	supplement, err := d.interactionControl(member, waiting.ID, "input", history.Content.Text, uuid.NewString(), map[string]any{"source_message_id": history.MessageID, "message_ids": []entityID{selected.MessageID, *otherRun.MessageID}, "file_ids": []entityID{file.info.FileID}, "history_grant_id": grant})
	if err != nil {
		return err
	}
	if supplement.InputVersion != 2 {
		return errors.New("interaction.material-input: rejected inputs consumed authoritative version")
	}
	held, err := d.interactionHeldRequest(materialWaitToken)
	if err != nil {
		return err
	}
	heldInputs, _, err := interactionInputs(held)
	if err != nil {
		return err
	}
	if len(heldInputs) != 2 || !hasEntityID(heldInputs[1].SourceMessageID, history.MessageID) || heldInputs[1].RequesterID != member.id {
		return errors.New("interaction.material-input: supplement source/participant attribution missing")
	}
	// A later message edit cannot rewrite the already accepted material snapshot.
	if err = d.request(http.MethodPut, "/messages/"+selected.MessageID.String(), owner.token, map[string]string{"text": "EDITED_AFTER_ACCEPTANCE VALUE=299"}, nil); err != nil {
		return err
	}
	for _, text := range []string{"MATERIAL_LATER_INPUT VALUE=2", "MATERIAL_FINAL_INPUT VALUE=3"} {
		if _, err = d.interactionControl(owner, waiting.ID, "input", text, uuid.NewString(), nil); err != nil {
			return err
		}
	}
	if err = d.interactionGate(materialWaitToken, "release"); err != nil {
		return err
	}
	materialDone, err := d.interactionWait(member, waiting.ID, "succeeded")
	if err != nil {
		return err
	}
	materialComputed, err := interactionDecodedResult(materialDone)
	if err != nil {
		return err
	}
	if materialComputed.Sum != 144 {
		return fmt.Errorf("interaction.material-input: expected fixed accepted snapshot sum 144, got %d", materialComputed.Sum)
	}
	materialCalls, err := d.interactionRequests(materialWaitToken)
	if err != nil {
		return err
	}
	for _, call := range materialCalls[1:] {
		if strings.Contains(string(call["messages"]), "EDITED_AFTER_ACCEPTANCE") {
			return errors.New("interaction.material-input: accepted source reloaded from current message body")
		}
	}
	// Leave another waiting Run to verify that rebinding fences pending input too.
	waiting, err = d.interactionStart(member, bot.ID, conv, "INTERACTION_WAIT_REBIND_"+suffix+" VALUE=1", nil)
	if err != nil {
		return err
	}
	waiting, err = d.interactionWait(member, waiting.ID, "waiting_input")
	if err != nil {
		return err
	}
	// Rebinding cannot carry an old grant, parent snapshot, or pending work forward.
	if err = d.request(http.MethodDelete, "/convs/"+conv.String()+"/bots/"+bot.ID.String(), owner.token, nil, nil); err != nil {
		return err
	}
	if err = d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", owner.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
		return err
	}
	rejoined, err := d.identityBinding(owner, conv, bot.ID)
	if err != nil {
		return err
	}
	if rejoined.BindingID == binding.BindingID {
		return errors.New("interaction.rebinding: old binding lifecycle reused")
	}
	for _, extra := range []map[string]any{{"history_grant_id": grant}, {"parent_run_id": materialRun.ID}, {"message_ids": []entityID{*materialRun.MessageID}}} {
		body := map[string]any{"conv_id": conv, "submission_key": uuid.NewString(), "message": "INTERACTION_OLD_BINDING VALUE=1"}
		for key, value := range extra {
			body[key] = value
		}
		if err = d.runtimeReject(http.MethodPost, "/bots/"+bot.ID.String()+"/runs", member, body, http.StatusForbidden); err != nil {
			return err
		}
	}
	if err = d.runtimeReject(http.MethodPost, "/runs/"+waiting.ID.String()+"/control", member, map[string]any{"operation": "input", "submission_key": uuid.NewString(), "message": "OLD_BINDING_INPUT VALUE=1"}, http.StatusForbidden); err != nil {
		return err
	}
	deniedRequests, err := d.interactionRequests("INTERACTION_DENIED_" + suffix)
	if err != nil {
		return err
	}
	if len(deniedRequests) != 0 {
		return errors.New("interaction.authorization: rejected reference reached model")
	}
	return nil
}

func (d *driver) interactionExecutors(owner, member account, conv entityID, suffix, address string) error {
	for _, mode := range []string{"ws", "webhook"} {
		var bot botView
		body := map[string]any{"name": "interaction-executor-" + mode + suffix, "type": "third_party", "sub_type": mode, "conn_mode": mode, "response_triggers": []string{"mention"}, "capabilities": `{"input_types":["text"],"supports_input":true,"supports_stop":false,"supports_resume":false}`}
		if mode == "webhook" {
			body["callback_url"] = d.provider + "/external-webhook"
		}
		if err := d.request(http.MethodPost, "/bots", owner.token, body, &bot); err != nil {
			return err
		}
		if err := d.request(http.MethodPost, "/convs/"+conv.String()+"/bots", owner.token, map[string]any{"bot_id": bot.ID}, nil); err != nil {
			return err
		}
		var x *controlledExecutor
		var err error
		if mode == "ws" {
			x, err = d.websocketExecutor(owner, bot.ID, address, "interaction-executor-"+suffix)
		} else {
			x, err = d.webhookExecutor(owner, bot.ID)
		}
		if err != nil {
			return err
		}
		if x.conn != nil {
			defer x.conn.Close()
		}
		started, err := d.startRun(owner, bot.ID, conv, "executor initial "+suffix, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		request, err := x.request(started.ID)
		if err != nil {
			return err
		}
		if _, err = x.update(executorPayload(request, "run.claim", "accepted", 0), false, false); err != nil {
			return err
		}
		acceptedInput, err := d.interactionControl(member, started.ID, "input", "accepted phase material "+suffix, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		if acceptedInput.InputVersion != request.InputVersion+1 {
			return errors.New("interaction.executor: accepted phase input was not appended")
		}
		request, err = x.requestVersion(started.ID, request.Generation, acceptedInput.InputVersion, "")
		if err != nil {
			return err
		}
		if _, err = x.update(executorPayload(request, "run.update", "waiting_input", 1), false, false); err != nil {
			return err
		}
		texts := []string{"ordered member material " + suffix, "ordered owner material " + suffix}
		callers := []account{member, owner}
		var receipts [2]interactionRun
		var failures [2]error
		var wg sync.WaitGroup
		for i := range receipts {
			wg.Go(func() {
				receipts[i], failures[i] = d.interactionControl(callers[i], started.ID, "input", texts[i], uuid.NewString(), nil)
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
		if !slices.Equal(versions, []int64{request.InputVersion + 1, request.InputVersion + 2}) {
			return errors.New("interaction.executor: concurrent supplements overwritten")
		}
		// Delivery can coalesce superseded snapshots, but the latest immutable
		// input log must retain every accepted contributor in authority order.
		version := versions[len(versions)-1]
		delivered, err := x.requestVersion(started.ID, request.Generation, version, "")
		if err != nil {
			return err
		}
		if len(delivered.Run.InputSources) != int(version) {
			return errors.New("interaction.executor: immutable delivery source version lost")
		}
		for i, receipt := range receipts {
			if !strings.Contains(delivered.Text(), texts[i]) {
				return errors.New("interaction.executor: accepted material absent from ordered delivery")
			}
			found := false
			for _, source := range delivered.Run.InputSources {
				if source.Version == receipt.InputVersion && source.RequesterID == callers[i].id {
					found = true
				}
			}
			if !found {
				return errors.New("interaction.executor: participant identity lost")
			}
		}
		request = delivered
		computed := executorText(request.Text())
		complete := executorPayload(request, "run.update", "completed", 2)
		complete["result_text"] = computed
		if _, err = x.update(complete, false, mode == "webhook"); err != nil {
			return err
		}
		done, err := d.publishedRun(member, started.ID)
		if err != nil {
			return err
		}
		if done.ResultText != computed {
			return errors.New("interaction.executor: actual accepted material computation mismatch")
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+started.ID.String()+"/control", member, map[string]any{"operation": "input", "message": "late input", "submission_key": uuid.NewString()}, http.StatusConflict); err != nil {
			return err
		}
		// Capabilities are snapped on a fresh Run; no silent unsupported acceptance.
		if err = d.request(http.MethodPut, "/bots/"+bot.ID.String(), owner.token, map[string]string{"capabilities": executorCapabilities}, nil); err != nil {
			return err
		}
		unsupported, err := d.startRun(member, bot.ID, conv, "unsupported input "+suffix, uuid.NewString(), nil)
		if err != nil {
			return err
		}
		unsupportedRequest, err := x.request(unsupported.ID)
		if err != nil {
			return err
		}
		if err = d.runtimeReject(http.MethodPost, "/runs/"+unsupported.ID.String()+"/control", member, map[string]any{"operation": "input", "message": "must reject", "submission_key": uuid.NewString()}, http.StatusConflict); err != nil {
			return err
		}
		unchanged, err := d.readRun(member, unsupported.ID)
		if err != nil {
			return err
		}
		if unchanged.InputVersion != unsupported.InputVersion || unchanged.ExecutorCapabilities.SupportsInput {
			return errors.New("interaction.executor: unsupported input mutated work")
		}
		text := executorText(unsupportedRequest.Text())
		if _, err = x.complete(unsupportedRequest, "text", map[string]any{"text": text}, text); err != nil {
			return err
		}
	}
	return nil
}
