package main

import (
	"errors"
	"fmt"
	"net/http"
)

func (f *knowledgeToolsFixture) readingBudget() (result error) {
	messages, grant, err := f.historySeed("BUDGET", 1)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, f.d.request(http.MethodPut, "/bots/"+f.bot.ID.String(), f.owner.token, map[string]any{"max_step": 8, "enable_knowledge": true, "streaming_enabled": true}, nil))
	}()
	for _, limit := range []int{1, 2} {
		if err = f.d.request(http.MethodPut, "/bots/"+f.bot.ID.String(), f.owner.token, map[string]any{"max_step": limit, "enable_knowledge": true, "streaming_enabled": true}, nil); err != nil {
			return err
		}
		for _, kind := range []string{"document", "history"} {
			var run publicRun
			var answer readingAnswer
			label := fmt.Sprintf("BUDGET_%s_%d", kind, limit)
			call := map[string]any{"tool": "read_knowledge_document", "doc_id": f.doc.ID, "limit": 1}
			if kind == "history" {
				call = map[string]any{"tool": "read_conversation_history", "limit": 1}
				run, answer, err = f.historyRun(label, grant, []map[string]any{call, call})
			} else {
				run, answer, err = f.readingRun(label, []map[string]any{call, call})
			}
			if err != nil {
				return err
			}
			if !answer.Bounded {
				return errors.New("reading budget exhaustion was not honestly reported by bounded delivery")
			}
			if limit == 1 {
				if !answer.Gap || len(answer.DocumentReads) != 0 || len(answer.HistoryReads) != 0 || len(run.Sources) != 0 {
					return errors.New("single-round reading budget invented evidence or initiated a read")
				}
			} else if kind == "history" {
				if len(answer.HistoryReads) != 1 || len(answer.HistoryReads[0].Items) != 1 || answer.HistoryReads[0].Items[0].MessageID != messages[0].MessageID || answer.HistoryReads[0].Items[0].Content != messages[0].Content.Text {
					return errors.New("bounded history delivery discarded the one completed real page or continued reading")
				}
				if err = f.historySourceEvidence(run.ID, messages); err != nil {
					return err
				}
			} else if len(answer.DocumentReads) != 1 || len(answer.DocumentReads[0].Items) != 1 || answer.DocumentReads[0].Items[0].DocID != f.doc.ID || answer.DocumentReads[0].Items[0].Content != f.content {
				return errors.New("bounded document delivery discarded the one completed real page or continued reading")
			}
		}
	}
	return nil
}
