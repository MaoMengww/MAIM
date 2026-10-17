package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// A real private upload is consumed by A, then B reads A's published answer
// without receiving A's file grant. Revoke only that original grant, and inspect
// B's message first: no read of A may prime cached access_revoked flags.
func (f *knowledgeToolsFixture) historyOriginalFileRevocation() (result error) {
	origin := f.authorizationFixture
	model, err := f.d.fixtureModel(f.owner, "chat", "fixture-authorization-history-origin-"+f.suffix)
	if err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/bots", f.owner.token, map[string]any{"name": "history-file-origin-" + f.suffix, "type": "self_deployed", "model_id": model, "enable_knowledge": false, "streaming_enabled": true, "response_triggers": []string{"mention"}}, &origin.bot); err != nil {
		return err
	}
	if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/bots", f.admin.token, map[string]any{"bot_id": origin.bot.ID}, nil); err != nil {
		return err
	}
	canary := "HISTORY_ORIGINAL_FILE_" + f.suffix
	file, err := f.d.attachmentUpload(f.requester, "history-origin-"+f.suffix+".txt", "text/plain", []byte(canary), 1)
	if err != nil {
		return err
	}
	grant, err := f.d.resourceGrant(f.requester, origin.bot.ID, f.conv, "file", file.info.FileID, "conversation", true)
	if err != nil {
		return err
	}
	token := "AUTH_HISTORY_ORIGIN_" + f.suffix
	if err = origin.arm(token, nil, false); err != nil {
		return err
	}
	started, err := origin.start(token, map[string]any{"file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{grant.ID}})
	if err != nil {
		return err
	}
	source, err := f.d.waitRun(f.requester, started.ID)
	if err != nil {
		return err
	}
	if source.Status != "succeeded" || source.MessageID == nil || !strings.Contains(source.ResultText, canary) {
		return errors.New("history origin did not consume and publish the actual private file")
	}
	if err = origin.modelContains(token, canary, true); err != nil {
		return err
	}
	message, err := f.d.identityMessage(f.member, *source.MessageID)
	if err != nil {
		return err
	}
	if !strings.Contains(message.Text.Text, canary) {
		return errors.New("authorized original file result was prematurely redacted")
	}
	binding, err := f.d.identityBinding(f.admin, f.conv, f.bot.ID)
	if err != nil {
		return err
	}
	history, err := f.d.identityGrant(f.requester, f.conv, f.bot.ID, binding.BindingID, message.Seq, message.Seq)
	if err != nil {
		return err
	}
	derived, answer, err := f.historyRun("ORIGINAL_FILE", history, []map[string]any{{"tool": "read_conversation_history", "limit": 1}})
	if err != nil {
		return err
	}
	if len(answer.HistoryReads) != 1 || len(answer.HistoryReads[0].Items) != 1 || answer.HistoryReads[0].Items[0].MessageID != message.MessageID || !strings.Contains(answer.HistoryReads[0].Items[0].Content, canary) || derived.MessageID == nil || !strings.Contains(derived.ResultText, canary) {
		return errors.New("B did not consume A's real file-derived history with its independent identity")
	}
	before, err := f.d.identityMessage(f.member, *derived.MessageID)
	if err != nil {
		return err
	}
	if !strings.Contains(before.Text.Text, canary) {
		return errors.New("B's independently authorized history result was prematurely redacted")
	}

	// Also leave a second B Run after its real tool read but before final model
	// delivery, so the same grant revocation exercises the consume/publish fence.
	heldToken := "KNOWLEDGE_HISTORY_ORIGINAL_FILE_INFLIGHT_" + f.suffix
	if err = f.plan(heldToken, []map[string]any{{"tool": "read_conversation_history", "limit": 1}}, 1, false); err != nil {
		return err
	}
	inflight, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, heldToken, map[string]any{"history_grant_id": history})
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, f.releaseKnowledge(heldToken)) }()
	if err = f.heldKnowledge(heldToken); err != nil {
		return err
	}
	observed, err := f.observeKnowledge(heldToken)
	if err != nil {
		return err
	}
	if len(observed.Requests) != 2 || !strings.Contains(string(observed.Requests[1]), canary) {
		return errors.New("inflight B did not actually read the original file-derived history")
	}
	if err = f.d.request(http.MethodPost, "/bot-authorizations/resources", f.requester.token, map[string]any{"op": "revoke", "grant_id": grant.ID}, nil); err != nil {
		return err
	}

	// This MUST be the first disclosure request after revoking A's grant.
	projected, err := f.d.identityMessage(f.member, *derived.MessageID)
	if err != nil {
		return err
	}
	if strings.Contains(projected.Text.Text, canary) || projected.RawPayload != "" {
		return errors.New("B message projection disclosed A's revoked file before any refresh of A")
	}
	if err = f.releaseKnowledge(heldToken); err != nil {
		return err
	}
	finished, err := f.d.waitRun(f.requester, inflight.ID)
	var rejected *apiError
	if err != nil && !(errors.As(err, &rejected) && rejected.status == http.StatusForbidden) {
		return err
	}
	if finished.MessageID != nil || finished.OutputStatus == "committed" {
		return errors.New("B published already-read history after A's original file grant was revoked")
	}
	current, err := f.d.readRun(f.requester, derived.ID)
	if err != nil && !(errors.As(err, &rejected) && rejected.status == http.StatusForbidden) {
		return err
	}
	if err == nil && strings.Contains(current.ResultText, canary) {
		return errors.New("B completed Run disclosed history after A's original file grant was revoked")
	}
	fmt.Printf("History original-source evidence: source=%s consumer=%s inflight=%s original_file_grant=%s; first consumer message projection redacted without refreshing source\n", source.ID, derived.ID, inflight.ID, grant.ID)
	return nil
}
