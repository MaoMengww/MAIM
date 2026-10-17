package repo

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Published messages remain historical facts, but a new client disclosure must
// not expose revoked knowledge, on-demand history or their derived answers.
// This projection never edits the persisted message or original accepted input.
func (r *MessageRepo) ProjectKnowledgeResults(ctx context.Context, messages []model.Message) error {
	if r.userID == "" {
		return nil
	}
	tx := r.db.WithContext(ctx)
	checked := make(map[string]bool)
	invalid := make(map[string]bool)
	for i := range messages {
		message := &messages[i]
		if message.SenderType != "bot" || message.RawPayload == "" {
			continue
		}
		var metadata struct {
			RunID   string            `json:"run_id"`
			Sources []json.RawMessage `json:"kb_sources"`
		}
		if err := json.Unmarshal([]byte(message.RawPayload), &metadata); err != nil {
			continue
		}
		if identity.Validate(metadata.RunID) != nil {
			continue // Third-party message metadata alone never establishes a Run.
		}
		if !checked[metadata.RunID] {
			var run struct {
				BotID, ConvID, BindingID, RequesterID, Visibility string
				ResourceGrantIDs                                  []string `gorm:"serializer:json"`
				Provenance                                        json.RawMessage
				AccessRevoked, ResultInvalidated                  bool
			}
			if err := tx.Table("bot.runs").Select("bot_id,conv_id,binding_id,requester_id,visibility,resource_grant_ids,provenance,access_revoked,result_invalidated").Where("id = ?", metadata.RunID).Take(&run).Error; err != nil {
				return err
			}
			if message.SenderID == nil || *message.SenderID != run.BotID || message.ConvID != run.ConvID || message.BindingID == nil || *message.BindingID != run.BindingID {
				return status.Error(codes.PermissionDenied, "knowledge result provenance does not match the message")
			}
			invalid[metadata.RunID] = run.AccessRevoked || run.ResultInvalidated || run.Visibility != "conversation"
			access := resourcegrant.Access{BotID: run.BotID, ConvID: run.ConvID, BindingID: run.BindingID, RequesterID: run.RequesterID, Visibility: run.Visibility, GrantIDs: run.ResourceGrantIDs}
			var versions []struct {
				resourcegrant.KnowledgeSource
				resourcegrant.HistorySource
			}
			if err := json.Unmarshal(run.Provenance, &versions); err != nil {
				return err
			}
			for _, source := range versions {
				if !invalid[metadata.RunID] {
					if err := resourcegrant.CheckHistorySource(tx, access, source.HistorySource, false); err != nil {
						if status.Code(err) != codes.PermissionDenied {
							return err
						}
						invalid[metadata.RunID] = true
					}
				}
				if source.KnowledgeID == "" || invalid[metadata.RunID] {
					continue
				}
				_, err := resourcegrant.Check(tx, access, "knowledge", source.KnowledgeID, true, false)
				if err == nil {
					err = resourcegrant.CheckKnowledgeSource(tx, source.KnowledgeSource, false)
				}
				if err != nil {
					if status.Code(err) != codes.PermissionDenied {
						return err
					}
					invalid[metadata.RunID] = true
				}
			}
			checked[metadata.RunID] = true
		}
		if invalid[metadata.RunID] {
			message.Content = model.JSONContent{"text": "资料来源已失效，此结果暂不可披露。"}
			message.MsgType = model.MsgTypeText
			message.RawPayload, message.EditHistory = "", nil
			message.DisclosureInvalidated = true
		}
	}
	return nil
}
