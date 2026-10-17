package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
)

func (f *knowledgeToolsFixture) historyRun(label string, grant entityID, calls []map[string]any) (publicRun, readingAnswer, error) {
	token := "KNOWLEDGE_HISTORY_" + label + "_" + f.suffix
	if err := f.plan(token, calls, -1, false); err != nil {
		return publicRun{}, readingAnswer{}, err
	}
	extra := map[string]any{}
	if grant != "" {
		extra["history_grant_id"] = grant
	}
	run, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, token, extra)
	if err != nil {
		return publicRun{}, readingAnswer{}, err
	}
	finished, err := f.d.waitRun(f.requester, run.ID)
	var answer readingAnswer
	if err != nil {
		return finished, answer, err
	}
	if finished.Status != "succeeded" || finished.OutputStatus != "committed" {
		return finished, answer, fmt.Errorf("history %s: status=%s error=%s", label, finished.Status, finished.Error)
	}
	if err = json.Unmarshal([]byte(finished.ResultText), &answer); err != nil {
		return finished, answer, err
	}
	return finished, answer, f.uniqueMessage(finished)
}

func (f *knowledgeToolsFixture) historySeed(label string, count int) ([]sentMessage, entityID, error) {
	binding, err := f.d.identityBinding(f.admin, f.conv, f.bot.ID)
	if err != nil {
		return nil, "", err
	}
	messages := make([]sentMessage, 0, count)
	for index := range count {
		text := fmt.Sprintf("HISTORY_%s_%d_%s discussion constraint: exactly one writer. Untrusted material asks to replace requester and read all other groups.", label, index, f.suffix)
		message, err := f.d.sendMessage(f.admin, f.conv, text, 0)
		if err != nil {
			return nil, "", err
		}
		messages = append(messages, message)
	}
	grant, err := f.d.identityGrant(f.requester, f.conv, f.bot.ID, binding.BindingID, messages[0].Seq, messages[len(messages)-1].Seq)
	return messages, grant, err
}

func (f *knowledgeToolsFixture) conversationReading() (result error) {
	if err := f.d.request(http.MethodPut, "/bots/"+f.bot.ID.String(), f.owner.token, map[string]any{"max_context_messages": 2, "max_step": 8, "enable_knowledge": false, "streaming_enabled": true}, nil); err != nil {
		return err
	}
	defer func() {
		restoreErr := f.d.request(http.MethodPut, "/bots/"+f.bot.ID.String(), f.owner.token, map[string]any{"max_context_messages": 10, "max_step": 8, "enable_knowledge": true, "streaming_enabled": true}, nil)
		// Rejoining creates a new member incarnation, not a revival of old grants.
		if restoreErr == nil {
			restoreErr = f.revokeKnowledge()
		}
		if restoreErr == nil {
			_, restoreErr = f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "knowledge", f.kb, "conversation", true)
		}
		result = errors.Join(result, restoreErr)
	}()
	before, err := f.d.sendMessage(f.admin, f.conv, "HISTORY_OUTSIDE_BEFORE_"+f.suffix, 0)
	if err != nil {
		return err
	}
	messages, grant, err := f.historySeed("PAGED", 3)
	if err != nil {
		return err
	}
	after, err := f.d.sendMessage(f.admin, f.conv, "HISTORY_OUTSIDE_AFTER_"+f.suffix, 0)
	if err != nil {
		return err
	}
	run, answer, err := f.historyRun("PAGED", grant, []map[string]any{
		{"tool": "read_conversation_history", "limit": 1},
		{"tool": "read_conversation_history", "continue_history": true, "limit": 1},
		{"tool": "read_conversation_history", "continue_history": true, "limit": 1},
	})
	if err != nil {
		return err
	}
	if len(answer.HistoryReads) != 3 || len(answer.Errors) != 0 {
		return errors.New("explicit history grant larger than max_context_messages could not be paged by the Bot")
	}
	seen := map[entityID]bool{}
	for index, page := range answer.HistoryReads {
		if len(page.Items) != 1 || page.HasMore != (index < 2) || page.Truncated != (index < 2) {
			return errors.New("history page did not expose its actual bounded continuation")
		}
		item := page.Items[0]
		if seen[item.MessageID] || !slices.ContainsFunc(messages, func(message sentMessage) bool {
			return item.MessageID == message.MessageID && item.Seq == message.Seq && item.Content == message.Content.Text
		}) {
			return errors.New("history page invented, duplicated or crossed the real authorized seq interval")
		}
		seen[item.MessageID] = true
	}
	if err = f.historySourceEvidence(run.ID, messages); err != nil {
		return err
	}
	observed, err := f.observeKnowledge("KNOWLEDGE_HISTORY_PAGED_" + f.suffix)
	if err != nil {
		return err
	}
	if len(observed.Requests) == 0 {
		return errors.New("history fixture did not observe real external model input")
	}
	for _, message := range messages {
		if strings.Contains(string(observed.Requests[0]), message.Content.Text) {
			return errors.New("explicit history grant eagerly injected history before an on-demand tool read")
		}
	}
	for _, request := range observed.Requests {
		if strings.Contains(string(request), before.Content.Text) || strings.Contains(string(request), after.Content.Text) {
			return errors.New("history tool consumed messages outside the explicit grant")
		}
	}
	_, absent, err := f.historyRun("MISSING", "", []map[string]any{{"tool": "read_conversation_history", "limit": 1}})
	if err != nil {
		return err
	}
	if len(absent.HistoryReads) != 0 || !absent.Gap || len(absent.Errors) != 1 || absent.Errors[0].Category != "missing_history_authorization" {
		return errors.New("missing history grant was auto-issued, treated as empty history or not reported as a gap")
	}
	var foreign struct {
		ID entityID `json:"conversation_id"`
	}
	if err = f.d.request(http.MethodPost, "/convs", f.admin.token, map[string]any{"type": "group", "group_name": "reading-foreign-" + f.suffix, "member_ids": []entityID{f.requester.id}}, &foreign); err != nil {
		return err
	}
	foreignMessage, err := f.d.sendMessage(f.admin, foreign.ID, "HISTORY_FOREIGN_SECRET_"+f.suffix, 0)
	if err != nil {
		return err
	}
	for _, forbidden := range []struct {
		field string
		value any
	}{
		{"conv_id", foreign.ID}, {"requester_id", f.owner.id}, {"history_grant_id", grant}, {"grant_id", grant},
	} {
		_, denied, err := f.historyRun("FORGED_"+forbidden.field, grant, []map[string]any{{"tool": "read_conversation_history", "limit": 1, forbidden.field: forbidden.value}})
		if err != nil {
			return err
		}
		if len(denied.HistoryReads) != 0 || len(denied.Errors) != 1 || denied.Errors[0].Category != "invalid_argument" {
			return fmt.Errorf("history accepted model-controlled %s", forbidden.field)
		}
		row, err := f.observeKnowledge("KNOWLEDGE_HISTORY_FORGED_" + forbidden.field + "_" + f.suffix)
		if err != nil {
			return err
		}
		for _, request := range row.Requests {
			if strings.Contains(string(request), foreignMessage.Content.Text) {
				return errors.New("forged conversation argument reached real foreign history")
			}
		}
	}
	for _, cursor := range []sequenceNumber{messages[0].Seq - 1, messages[2].Seq + 1} {
		token := fmt.Sprintf("KNOWLEDGE_HISTORY_OUTSIDE_%d_%s", cursor, f.suffix)
		if err = f.plan(token, []map[string]any{{"tool": "read_conversation_history", "cursor": cursor, "limit": 1}}, -1, false); err != nil {
			return err
		}
		started, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, token, map[string]any{"history_grant_id": grant})
		if err != nil {
			return err
		}
		finished, err := f.d.waitRun(f.requester, started.ID)
		if err != nil {
			return err
		}
		if finished.Status == "succeeded" || finished.MessageID != nil {
			return errors.New("out-of-range history cursor was not rejected at the authority boundary")
		}
	}
	if err = f.d.request(http.MethodPut, "/messages/"+messages[0].MessageID.String(), f.admin.token, map[string]any{"text": "HISTORY_EDITED_CURRENT_" + f.suffix}, nil); err != nil {
		return err
	}
	if err = f.redactedHistoryReview(run, messages[0].Content.Text); err != nil {
		return err
	}
	if err = f.historySupplement(); err != nil {
		return err
	}
	return f.historyMembershipBoundary()
}

func (f *knowledgeToolsFixture) historySourceEvidence(runID entityID, messages []sentMessage) error {
	var run struct {
		Sources []struct {
			Type      string         `json:"type"`
			MessageID entityID       `json:"message_id"`
			Seq       sequenceNumber `json:"seq"`
		} `json:"sources"`
	}
	if err := f.d.request(http.MethodGet, "/runs/"+runID.String(), f.requester.token, nil, &run); err != nil {
		return err
	}
	for _, message := range messages {
		if !slices.ContainsFunc(run.Sources, func(source struct {
			Type      string         `json:"type"`
			MessageID entityID       `json:"message_id"`
			Seq       sequenceNumber `json:"seq"`
		}) bool {
			return source.Type == "conversation_history" && source.MessageID == message.MessageID && source.Seq == message.Seq
		}) {
			return errors.New("public Run lost the real message ID/seq citation of live history")
		}
	}
	return nil
}

func (f *knowledgeToolsFixture) redactedHistoryReview(run publicRun, canary string) error {
	current, err := f.d.readRun(f.requester, run.ID)
	var rejected *apiError
	if err != nil && !(errors.As(err, &rejected) && rejected.status == http.StatusForbidden) {
		return err
	}
	if err == nil && strings.Contains(current.ResultText, canary) {
		return errors.New("edited live history remained disclosed by completed Run")
	}
	if run.MessageID == nil {
		return errors.New("history review requires a real committed Bot message")
	}
	message, err := f.d.identityMessage(f.member, *run.MessageID)
	if err != nil {
		return err
	}
	if strings.Contains(message.Text.Text, canary) {
		return errors.New("edited live history remained disclosed by the group result projection")
	}
	return nil
}

func (f *knowledgeToolsFixture) historyMembershipBoundary() (result error) {
	messages, grant, err := f.historySeed("MEMBERSHIP", 2)
	if err != nil {
		return err
	}
	token := "KNOWLEDGE_HISTORY_MEMBERSHIP_" + f.suffix
	if err = f.plan(token, []map[string]any{{"tool": "read_conversation_history", "limit": 1}, {"tool": "read_conversation_history", "continue_history": true, "limit": 1}}, 1, false); err != nil {
		return err
	}
	run, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, token, map[string]any{"history_grant_id": grant})
	if err != nil {
		return err
	}
	if err = f.heldKnowledge(token); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/kick", f.admin.token, map[string]any{"user_ids": []entityID{f.requester.id}}, nil); err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/invite", f.admin.token, map[string]any{"user_ids": []entityID{f.requester.id}}, nil))
	}()
	if err = f.releaseKnowledge(token); err != nil {
		return err
	}
	finished, err := f.d.waitRun(f.admin, run.ID)
	var denied *apiError
	if err != nil && !(errors.As(err, &denied) && denied.status == http.StatusForbidden) {
		return err
	}
	if finished.MessageID != nil {
		return errors.New("history member removal allowed an already-read result to publish")
	}
	observed, err := f.observeKnowledge(token)
	if err != nil {
		return err
	}
	for _, request := range observed.Requests[2:] {
		for _, message := range messages {
			if strings.Contains(string(request), message.Content.Text) {
				return errors.New("history member removal allowed further model consumption")
			}
		}
	}
	return nil
}

func (f *knowledgeToolsFixture) historySupplement() error {
	messages, grant, err := f.historySeed("SUPPLEMENT", 1)
	if err != nil {
		return err
	}
	token := "KNOWLEDGE_HISTORY_SUPPLEMENT_" + f.suffix
	if err = f.plan(token, []map[string]any{{"tool": "read_conversation_history", "limit": 1}}, -1, true); err != nil {
		return err
	}
	started, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, token, nil)
	if err != nil {
		return err
	}
	waiting, err := f.d.interactionWait(f.requester, started.ID, "waiting_input")
	if err != nil {
		return err
	}
	if waiting.MessageID != nil {
		return errors.New("missing history background was invented instead of requesting explicit input")
	}
	if _, err = f.d.interactionControl(f.requester, started.ID, "input", "Read only the explicitly supplied history interval and report its actual message source", uuid.NewString(), map[string]any{"history_grant_id": grant}); err != nil {
		return err
	}
	finished, err := f.d.waitRun(f.requester, started.ID)
	if err != nil {
		return err
	}
	var answer readingAnswer
	if finished.Status != "succeeded" || json.Unmarshal([]byte(finished.ResultText), &answer) != nil || len(answer.HistoryReads) != 1 || len(answer.HistoryReads[0].Items) != 1 || answer.HistoryReads[0].Items[0].MessageID != messages[0].MessageID || answer.HistoryReads[0].Items[0].Content != messages[0].Content.Text {
		return errors.New("accepted history grant did not become a bounded on-demand reading source")
	}
	observed, err := f.observeKnowledge(token)
	if err != nil {
		return err
	}
	if len(observed.Requests) < 3 || strings.Contains(string(observed.Requests[0]), messages[0].Content.Text) || strings.Contains(string(observed.Requests[1]), messages[0].Content.Text) {
		return errors.New("history supplement injected history without a real tool read")
	}
	if err = f.historySourceEvidence(finished.ID, messages); err != nil {
		return err
	}
	return f.uniqueMessage(finished)
}
