package run

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	msg "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type historyReadItem struct {
	MessageID   string  `json:"message_id"`
	Seq         int64   `json:"seq"`
	MessageType string  `json:"message_type"`
	SenderID    *string `json:"sender_id,omitempty"`
	SenderType  string  `json:"sender_type"`
	Content     string  `json:"content"`
}

type historyReadResult struct {
	Status     string            `json:"status"`
	Items      []historyReadItem `json:"items"`
	NextCursor int64             `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
	Truncated  bool              `json:"truncated"`
}

// Only explicit inputs of this Run can select its history permission. Neither
// inherited interaction materials nor model parameters can mint or widen it.
func runHistoryGrant(r *model.Run) string {
	for i := len(r.InputSources) - 1; i >= 0; i-- {
		if grant := r.InputSources[i].HistoryGrantID; grant != nil {
			return *grant
		}
	}
	if r.HistoryGrantID != nil {
		return *r.HistoryGrantID
	}
	return ""
}

func (s *Service) executeHistoryRead(ctx context.Context, r *model.Run, arguments string) (*knowledgeSearchOutcome, error) {
	args, err := decodeHistoryRead(arguments)
	if err != nil {
		return knowledgeFailure("invalid_argument"), nil
	}
	grant := runHistoryGrant(r)
	if grant == "" {
		return knowledgeFailure("missing_history_authorization"), nil
	}
	if s.messages == nil {
		return knowledgeFailure("unavailable"), nil
	}
	authorization, err := resourcegrant.CheckHistoryGrant(s.svc.DB.WithContext(ctx), resourceAccess(r), grant, false)
	if err != nil {
		return nil, err
	}
	if args.Cursor != 0 && (args.Cursor < authorization.StartSeq || args.Cursor > authorization.EndSeq) {
		return nil, status.Error(codes.PermissionDenied, "history cursor is outside the explicit grant")
	}
	result, err := s.messages.GetMessages(serviceContext(ctx), &msg.GetMessagesReq{ConversationId: r.ConvID, BotId: &r.BotID, BindingId: &r.BindingID, HistoryGrantId: &grant, Pagination: &msg.MessagePagination{Cursor: args.Cursor, Limit: args.Limit}})
	if err != nil {
		return knowledgeReadError(ctx, err)
	}
	if result == nil || len(result.Messages) > int(args.Limit) {
		return knowledgeFailure("unavailable"), nil
	}
	response := historyReadResult{Status: "ok", Items: []historyReadItem{}, HasMore: result.GetPagination().GetHasMore()}
	outcome := &knowledgeSearchOutcome{}
	input := Input{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: r.RequesterID}
	last := int64(0)
	outputBytes := 512
	for _, item := range result.Messages {
		if item == nil || item.ConversationId != r.ConvID || item.Seq < authorization.StartSeq || item.Seq > authorization.EndSeq || args.Cursor != 0 && item.Seq >= args.Cursor || last != 0 && item.Seq >= last {
			return nil, status.Error(codes.PermissionDenied, "history page is outside its authorized range")
		}
		// A grant author's visibility does not replace the Run requester's own
		// message and derived-result visibility. Do not hydrate replies or files.
		current, err := s.source(ctx, input, item.MessageId, true)
		if err != nil {
			return nil, err
		}
		if current.Seq != item.Seq || current.EditCount != item.EditCount {
			return nil, status.Error(codes.Aborted, "history source changed during reading")
		}
		material, err := messageMaterial(current, "conversation_history")
		if err != nil {
			return nil, err
		}
		material.HistoryGrantID, material.RequesterID, material.SourceExplicit = grant, r.RequesterID, true
		versions := []model.SourceVersion{{MessageID: material.MessageID, AcceptedEditCount: material.EditCount, HistoryGrantID: grant, HistorySeq: material.Seq}}
		if current.SenderType == "bot" && current.RawPayload != "" {
			var payload struct {
				RunID string `json:"run_id"`
			}
			if json.Unmarshal([]byte(current.RawPayload), &payload) == nil && identity.Validate(payload.RunID) == nil {
				origin, err := s.Get(ctx, payload.RunID, r.RequesterID)
				if err != nil {
					return nil, err
				}
				if origin.ConvID != r.ConvID || origin.MessageID == nil || *origin.MessageID != current.MessageId || origin.ResultInvalidated {
					return nil, status.Error(codes.PermissionDenied, "history result source unavailable")
				}
			}
		}
		material.Provenance, err = json.Marshal(versions)
		if err != nil {
			return nil, err
		}
		read := historyReadItem{MessageID: material.MessageID, Seq: material.Seq, MessageType: material.MessageType, SenderID: material.SenderID, SenderType: material.SenderType, Content: material.Content}
		encoded, err := json.Marshal(read)
		if err != nil {
			return nil, err
		}
		if outputBytes+len(encoded)+1 > knowledgeResultLimit {
			if len(response.Items) == 0 {
				return knowledgeFailure("reading_too_large"), nil
			}
			response.HasMore, response.Truncated = true, true
			break
		}
		outputBytes += len(encoded) + 1
		response.Items = append(response.Items, read)
		last = item.Seq
		outcome.Materials = append(outcome.Materials, material)
		outcome.Versions = append(outcome.Versions, versions...)
		source := materialSource(material)
		source["seq"] = material.Seq
		outcome.Sources = append(outcome.Sources, source)
	}
	if response.HasMore && len(response.Items) > 0 {
		response.NextCursor = response.Items[len(response.Items)-1].Seq
	}
	response.Truncated = response.HasMore
	raw, err := json.Marshal(response)
	outcome.Result = string(raw)
	return outcome, err
}
