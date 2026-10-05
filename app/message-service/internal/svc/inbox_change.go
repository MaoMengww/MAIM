package svc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/consts"
	"gorm.io/gorm"
)

// PublishInboxChange commits a durable change to the conversation's ordered channel.
// Callers hold the conversation lock so change publication follows mutation order.
func (s *ServiceContext) PublishInboxChange(ctx context.Context, tx *gorm.DB, payload map[string]any) error {
	convID, ok := payload["conv_id"].(int64)
	if !ok || convID <= 0 {
		return fmt.Errorf("inbox change requires a conversation")
	}
	switch payload["kind"] {
	case model.InboxMessageNew, model.InboxMessageEdited, model.InboxMessageRecalled, model.InboxMessageDeleted:
		// Snapshot recipients under the mutation's conversation lock. A retry after
		// someone joins must not allocate a fresh position for an old event.
		var recipients []int64
		if err := tx.WithContext(ctx).Model(&model.ConversationMember{}).
			Where("conv_id = ? AND member_type = ?", convID, model.MemberTypeUser).
			Order("user_id").Pluck("user_id", &recipients).Error; err != nil {
			return err
		}
		payload["recipient_ids"] = recipients
	}
	id, err := s.Snowflake.Generate()
	if err != nil {
		return err
	}
	payload["change_id"] = id
	evt := &model.OutboxEvent{ID: id, Topic: consts.KafkaTopicMessageCreated,
		Key: strconv.FormatInt(convID, 10), MaxRetries: model.DefaultMaxRetries}
	if err := evt.SetPayload(payload); err != nil {
		return err
	}
	return s.OutboxRepo.Insert(ctx, tx, evt)
}
