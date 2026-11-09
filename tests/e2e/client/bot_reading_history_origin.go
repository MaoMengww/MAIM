package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// A real private upload is consumed by A, then B reads A's published answer
// without receiving A's file grant. Revoking that source grant cannot revoke
// either independent result or a later direct grant to A's published message.
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

	// hold_at=1 observes the completed history tool payload in the second model
	// request, then blocks its response before final delivery.
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

	// Read B first: retained visibility must not depend on refreshing A.
	projected, err := f.d.identityMessage(f.member, *derived.MessageID)
	if err != nil {
		return err
	}
	if projected.Text.Text != before.Text.Text || !strings.Contains(projected.Text.Text, canary) {
		return errors.New("B message projection recursively revoked A's independent file-derived result")
	}
	if err = f.releaseKnowledge(heldToken); err != nil {
		return err
	}
	finished, err := f.d.waitRun(f.requester, inflight.ID)
	if err != nil {
		return err
	}
	if err = f.retainedHistoryReview(finished, canary); err != nil {
		return err
	}
	if err = f.uniqueMessage(finished); err != nil {
		return err
	}
	if err = f.retainedHistoryReview(derived, canary); err != nil {
		return err
	}
	if err = f.retainedHistoryReview(source, canary); err != nil {
		return err
	}
	// A newly issued grant targets B's current message only. Reading B must
	// neither require A's history grant nor recurse to A's revoked file grant.
	directGrant, err := f.d.identityGrant(f.requester, f.conv, f.bot.ID, binding.BindingID, projected.Seq, projected.Seq)
	if err != nil {
		return err
	}
	fresh, direct, err := f.historyRun("ORIGINAL_FILE_AFTER_REVOKE", directGrant, []map[string]any{{"tool": "read_conversation_history", "limit": 1}})
	if err != nil {
		return err
	}
	if len(direct.HistoryReads) != 1 || len(direct.HistoryReads[0].Items) != 1 || direct.HistoryReads[0].Items[0].MessageID != projected.MessageID || !strings.Contains(direct.HistoryReads[0].Items[0].Content, canary) {
		return errors.New("direct history authorization recursively required the revoked original file grant")
	}
	if err = f.retainedHistoryReview(fresh, canary); err != nil {
		return err
	}
	if err = f.d.authorizationReject("/bots/"+origin.bot.ID.String()+"/runs", f.requester.token, map[string]any{"conv_id": f.conv, "message": token + "_NEW_FILE_READ", "submission_key": uuid.NewString(), "file_ids": []entityID{file.info.FileID}, "resource_grant_ids": []entityID{grant.ID}}); err != nil {
		return err
	}
	fmt.Printf("History snapshot evidence: source=%s consumer=%s inflight=%s fresh=%s revoked_file_grant=%s; independent results retained, direct history allowed, fresh source read denied\n", source.ID, derived.ID, inflight.ID, fresh.ID, grant.ID)
	return nil
}
