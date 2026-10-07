package svc

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PublishInboxChange commits a durable change to the conversation's ordered channel.
// Callers hold the conversation lock so change publication follows mutation order.
func (s *ServiceContext) PublishInboxChange(ctx context.Context, tx *gorm.DB, payload map[string]any) error {
	convID, ok := payload["conv_id"].(string)
	if !ok || identity.Validate(convID) != nil {
		return fmt.Errorf("inbox change requires a conversation")
	}
	var conv model.Conversation
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", convID).Take(&conv).Error; err != nil {
		return err
	}
	next, err := sequence.Next(conv.PublicationSequence)
	if err != nil {
		return fmt.Errorf("conversation publication sequence: %w", err)
	}
	switch payload["kind"] {
	case model.InboxMessageNew, model.InboxMessageEdited, model.InboxMessageRecalled, model.InboxMessageDeleted:
		if _, scoped := payload["recipient_ids"]; scoped {
			break
		}
		// Snapshot recipients under the mutation's conversation lock so retries
		// never add a newly joined member to an old change.
		var recipients []string
		if err := tx.WithContext(ctx).Model(&model.ConversationMember{}).
			Where("conv_id = ? AND member_type = ?", convID, model.MemberTypeUser).
			Order("user_id").Pluck("user_id", &recipients).Error; err != nil {
			return err
		}
		payload["recipient_ids"] = recipients
	}
	id, err := identity.New()
	if err != nil {
		return err
	}
	payload["change_id"] = id
	payload["publication_sequence"] = next
	evt := &model.OutboxEvent{ID: id, ConvID: convID, PublicationSequence: next,
		Topic: consts.KafkaTopicMessageCreated, Key: convID, MaxRetries: model.DefaultMaxRetries}
	if err := evt.SetPayload(payload); err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&model.Conversation{}).Where("id = ?", convID).
		Update("publication_sequence", next).Error; err != nil {
		return err
	}
	return s.OutboxRepo.Insert(ctx, tx, evt)
}
