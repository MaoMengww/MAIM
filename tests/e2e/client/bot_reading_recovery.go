package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
)

func (f *knowledgeToolsFixture) documentAuthority() error {
	for _, kind := range []string{"list", "read"} {
		token := "KNOWLEDGE_READING_REVOKE_" + kind + "_" + f.suffix
		call := map[string]any{"tool": "read_knowledge_document", "doc_id": f.doc.ID, "limit": 1}
		if kind == "list" {
			call = map[string]any{"tool": "list_knowledge_documents", "kb_id": f.kb, "title": f.doc.Title, "limit": 1}
		}
		unauthorized := "KNOWLEDGE_READING_UNGRANTED_" + kind + "_" + f.suffix
		if err := f.plan(unauthorized, []map[string]any{call}, -1, false); err != nil {
			return err
		}
		ungranted, err := f.d.interactionStart(f.requester, f.bot.ID, f.conv, unauthorized, nil)
		if err != nil {
			return err
		}
		deniedRun, err := f.d.waitRun(f.requester, ungranted.ID)
		if err != nil {
			return err
		}
		if strings.Contains(deniedRun.ResultText, f.content) || strings.Contains(deniedRun.ResultText, f.doc.Title) || slices.ContainsFunc(deniedRun.Sources, func(source runtimeKnowledgeSource) bool { return source.KBID == f.kb }) {
			return errors.New("knowledge binding silently authorized an ungranted document read or directory title")
		}
		if err := f.plan(token, []map[string]any{call}, 1, false); err != nil {
			return err
		}
		run, err := f.startKnowledge(token, nil)
		if err != nil {
			return err
		}
		if err = f.heldKnowledge(token); err != nil {
			return err
		}
		if err = f.revokeKnowledge(); err != nil {
			return err
		}
		if err = f.releaseKnowledge(token); err != nil {
			return err
		}
		finished, err := f.d.waitRun(f.requester, run.ID)
		var denied *apiError
		if err != nil && !(errors.As(err, &denied) && denied.status == http.StatusForbidden) {
			return err
		}
		if finished.MessageID != nil || strings.Contains(finished.ResultText, f.content) || strings.Contains(finished.ResultText, f.doc.Title) {
			return errors.New("revoked document content or directory title was disclosed by the group result")
		}
		observed, err := f.observeKnowledge(token)
		if err != nil {
			return err
		}
		for _, request := range observed.Requests[2:] {
			if strings.Contains(string(request), f.content) || strings.Contains(string(request), f.doc.Title) {
				return errors.New("revoked document facts were reused by later external reasoning")
			}
		}
		if _, err = f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "knowledge", f.kb, "conversation", true); err != nil {
			return err
		}
	}
	return nil
}

func (f *knowledgeToolsFixture) restoreReading() error {
	for _, kind := range []string{"document", "history", "document-revoked", "history-revoked"} {
		token := "KNOWLEDGE_READING_RESTORE_" + kind + "_" + f.suffix
		call := map[string]any{"tool": "read_knowledge_document", "doc_id": f.doc.ID, "limit": 1}
		var messages []sentMessage
		extra := map[string]any{}
		if strings.HasPrefix(kind, "history") {
			var grant entityID
			var err error
			messages, grant, err = f.historySeed("RESTORE", 1)
			if err != nil {
				return err
			}
			extra["history_grant_id"] = grant
			call = map[string]any{"tool": "read_conversation_history", "limit": 1}
		}
		if err := f.plan(token, []map[string]any{call}, 1, false); err != nil {
			return err
		}
		run, err := f.startKnowledge(token, extra)
		if err != nil {
			return err
		}
		if err = f.heldKnowledge(token); err != nil {
			return err
		}
		if err = f.checkpoint("reading-"+kind+"-kill", run.ID); err != nil {
			return err
		}
		if kind == "document-revoked" {
			// Knowledge owns this binding; its public API remains available while
			// the Bot runtime (including resource-authorization forwarding) is dead.
			if err = f.d.request(http.MethodDelete, "/convs/"+f.conv.String()+"/knowledge/"+f.kb.String(), f.requester.token, nil, nil); err != nil {
				return err
			}
		}
		if kind == "history-revoked" {
			if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/kick", f.admin.token, map[string]any{"user_ids": []entityID{f.requester.id}}, nil); err != nil {
				return err
			}
		}
		if err = f.releaseKnowledge(token); err != nil {
			return err
		}
		if strings.HasSuffix(kind, "-revoked") {
			err = f.checkpoint("reading-"+kind+"-start", run.ID, http.StatusForbidden)
		} else {
			err = f.checkpoint("reading-"+kind+"-start", run.ID)
		}
		if err != nil {
			return err
		}
		if strings.HasSuffix(kind, "-revoked") {
			finished, err := f.d.waitRun(f.admin, run.ID)
			var denied *apiError
			if err != nil && !(errors.As(err, &denied) && denied.status == http.StatusForbidden) {
				return err
			}
			if finished.MessageID != nil || strings.Contains(finished.ResultText, f.content) || len(messages) > 0 && strings.Contains(finished.ResultText, messages[0].Content.Text) {
				return errors.New("restart treated old document/history authority as permanent disclosure permission")
			}
			observed, err := f.observeKnowledge(token)
			if err != nil {
				return err
			}
			for _, request := range observed.Requests[2:] {
				if strings.Contains(string(request), f.content) || len(messages) > 0 && strings.Contains(string(request), messages[0].Content.Text) {
					return errors.New("restart consumed a persisted reading after its authorization was lost")
				}
			}
			if kind == "history-revoked" {
				if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/members/invite", f.admin.token, map[string]any{"user_ids": []entityID{f.requester.id}}, nil); err != nil {
					return err
				}
				if err = f.revokeKnowledge(); err != nil {
					return err
				}
				if _, err = f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "knowledge", f.kb, "conversation", true); err != nil {
					return err
				}
			} else {
				if err = f.d.request(http.MethodPost, "/convs/"+f.conv.String()+"/knowledge", f.requester.token, map[string]any{"kb_id": f.kb}, nil); err != nil {
					return err
				}
				if err = f.revokeKnowledge(); err != nil {
					return err
				}
				if _, err = f.d.resourceGrant(f.requester, f.bot.ID, f.conv, "knowledge", f.kb, "conversation", true); err != nil {
					return err
				}
			}
			continue
		}
		finished, err := f.wait(run.ID, 2*f.d.timeout, func(run publicRun) bool { return run.Status == "succeeded" || run.Status == "failed" })
		if err != nil {
			return err
		}
		var answer readingAnswer
		if finished.Status != "succeeded" || json.Unmarshal([]byte(finished.ResultText), &answer) != nil {
			return errors.New("restored completed reading could not deliver its persisted real facts")
		}
		if kind == "document" {
			if len(answer.DocumentReads) != 1 || len(answer.DocumentReads[0].Items) != 1 {
				return errors.New("restart duplicated or discarded a completed document read")
			}
			item := answer.DocumentReads[0].Items[0]
			if item.DocID != f.doc.ID || item.KBID != f.kb || !strings.Contains(item.Content, f.content) || !slices.ContainsFunc(finished.Sources, func(source runtimeKnowledgeSource) bool {
				return source.DocID == item.DocID && source.ChunkID == item.ChunkID
			}) {
				return errors.New("restored document read lost the actual authorized chunk content/citation")
			}
		} else {
			if len(answer.HistoryReads) != 1 || len(answer.HistoryReads[0].Items) != 1 || answer.HistoryReads[0].Items[0].MessageID != messages[0].MessageID || answer.HistoryReads[0].Items[0].Content != messages[0].Content.Text {
				return errors.New("restart duplicated, discarded or widened a completed history read")
			}
			if err = f.historySourceEvidence(finished.ID, messages); err != nil {
				return err
			}
		}
		if err = f.uniqueMessage(finished); err != nil {
			return err
		}
	}
	return nil
}
