package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (f *knowledgeToolsFixture) readingCompaction() (result error) {
	modelID, err := f.d.fixtureModel(f.owner, "chat", "fixture-compaction-reading-"+f.suffix)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodPut, "/bots/"+f.bot.ID.String(), f.owner.token, map[string]any{"model_id": f.model, "max_context_tokens": 32768, "max_step": 8, "enable_knowledge": true, "streaming_enabled": true}, nil))
	}()
	for _, kind := range []string{"document", "history"} {
		if err = f.d.request(http.MethodPut, "/bots/"+f.bot.ID.String(), f.owner.token, map[string]any{"model_id": f.model, "max_context_tokens": 32768, "max_step": 8, "enable_knowledge": true, "streaming_enabled": true}, nil); err != nil {
			return err
		}
		canary := "CANARY_READING_" + strings.ToUpper(kind)
		var parent publicRun
		var source sentMessage
		if kind == "history" {
			source, err = f.d.sendMessage(f.admin, f.conv, canary+" approved discussion constraint", 0)
			if err != nil {
				return err
			}
			binding, err := f.d.identityBinding(f.admin, f.conv, f.bot.ID)
			if err != nil {
				return err
			}
			grant, err := f.d.identityGrant(f.requester, f.conv, f.bot.ID, binding.BindingID, source.Seq, source.Seq)
			if err != nil {
				return err
			}
			parent, _, err = f.historyRun("COMPACT", grant, []map[string]any{{"tool": "read_conversation_history", "limit": 1}})
		} else {
			document, uploadErr := f.d.uploadKnowledge(f.requester, f.kb, "compaction-reading.txt", canary, canary+" approved document constraint")
			if uploadErr != nil {
				return uploadErr
			}
			defer func() {
				result = errors.Join(result, f.d.request(http.MethodDelete, "/knowledge/documents/"+document.ID.String(), f.requester.token, nil, nil))
			}()
			if err = f.d.awaitKnowledgeDocument(f.requester, document, "ready", time.Now().Add(f.d.ingestTimeout)); err != nil {
				return err
			}
			parent, _, err = f.readingRun("COMPACT", []map[string]any{{"tool": "read_knowledge_document", "doc_id": document.ID, "limit": 1}})
		}
		if err != nil {
			return err
		}
		if err = f.d.request(http.MethodPut, "/bots/"+f.bot.ID.String(), f.owner.token, map[string]any{"model_id": modelID, "max_context_tokens": 11000, "max_step": 16, "enable_knowledge": true, "streaming_enabled": true}, nil); err != nil {
			return err
		}
		cp := compactionFixture{d: f.d, owner: f.requester, member: f.admin, bot: f.bot, conv: f.conv, suffix: f.suffix}
		token := "CP_READING" + strings.ToUpper(kind) + "_" + f.suffix
		if _, err = cp.gate(token, "arm", "summary"); err != nil {
			return err
		}
		child, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, token+" VALUE=5"+compactionPadding(0), map[string]any{"parent_run_id": parent.ID})
		if err != nil {
			return err
		}
		if _, err = f.d.interactionWait(f.requester, child.ID, "waiting_input"); err != nil {
			return err
		}
		if _, _, _, err = cp.driveSummary(child, token); err != nil {
			return err
		}
		if _, err = cp.gate(token, "release", ""); err != nil {
			return err
		}
		if _, err = f.d.interactionWait(f.requester, child.ID, "waiting_input"); err != nil {
			return err
		}
		observed, err := cp.gate(token, "observe", "")
		if err != nil {
			return err
		}
		var latest compactionRequest
		for _, request := range observed.Requests {
			if request.Kind == "work" {
				latest = request
			}
		}
		view, _, err := compactionView(latest)
		if err != nil || !strings.Contains(view, canary) {
			return fmt.Errorf("%s reading did not survive into a real summary consumed by subsequent reasoning: %w", kind, err)
		}
		if _, err = cp.input(f.requester, child.ID, "COMPLETE VALUE=17", nil); err != nil {
			return err
		}
		finished, err := f.d.interactionWait(f.requester, child.ID, "succeeded")
		if err != nil {
			return err
		}
		var answer struct {
			Materials []string `json:"materials"`
		}
		if json.Unmarshal([]byte(finished.ResultText), &answer) != nil || !slices.Contains(answer.Materials, canary) {
			return errors.New("compressed reading final answer lost the real source fact")
		}
		if kind == "history" {
			if err = f.d.request(http.MethodPut, "/messages/"+source.MessageID.String(), f.admin.token, map[string]any{"text": "replaced history source"}, nil); err != nil {
				return err
			}
		} else if err = f.revokeKnowledge(); err != nil {
			return err
		}
		if err = f.redactedHistoryReview(finished.publicRun, canary); err != nil {
			return err
		}
		if kind == "document" {
			if _, err = f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "knowledge", f.kb, "conversation", true); err != nil {
				return err
			}
		}
	}
	return nil
}
