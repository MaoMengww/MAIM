package messageservicelogic

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockMutableMessage follows the send path's conversation-first lock order.
// Removal cannot race authorization, nor can concurrent edits lose edit history.
func lockMutableMessage(ctx context.Context, s *svc.ServiceContext, tx *gorm.DB, convID, messageID, userID string) (*model.Message, error) {
	permission, err := s.ConversationRepo.CheckSendPermission(ctx, tx, convID, userID, model.MemberTypeUser)
	if err != nil {
		return nil, err
	}
	if !permission.IsMember {
		return nil, ErrNotMember
	}
	var msg model.Message
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conv_id = ?", messageID, convID).Take(&msg).Error; err != nil {
		return nil, err
	}
	if msg.SenderID == nil || *msg.SenderID != userID {
		return nil, errors.New(errors.CodeForbidden, "only sender can change a message")
	}
	return &msg, nil
}

func publishMessageChange(ctx context.Context, s *svc.ServiceContext, tx *gorm.DB, msg *model.Message, kind string) error {
	// Keep the conversation preview consistent with the current tail. MaxSeq is
	// an immutable conversation sequence boundary even when its last row is deleted.
	var conv model.Conversation
	if err := tx.Where("id = ?", msg.ConvID).Take(&conv).Error; err != nil {
		return err
	}
	if conv.LastMessageID != nil && *conv.LastMessageID == msg.ID {
		lastID, preview := &msg.ID, model.MessagePreview(msg.MsgType, msg.Content)
		switch kind {
		case model.InboxMessageRecalled:
			preview = "[消息已撤回]"
		case model.InboxMessageDeleted:
			var latest model.Message
			if err := tx.Where("conv_id = ?", msg.ConvID).Order("seq DESC").Limit(1).Find(&latest).Error; err != nil {
				return err
			}
			lastID, preview = nil, ""
			if latest.ID != "" {
				lastID = &latest.ID
				preview = model.MessagePreview(latest.MsgType, latest.Content)
				if latest.Status == model.MessageStatusRecalled {
					preview = "[消息已撤回]"
				}
			}
		}
		if err := tx.Model(&model.Conversation{}).Where("id = ?", msg.ConvID).Updates(map[string]any{
			"last_message_id": lastID, "last_message_preview": preview, "updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}
	}
	payload := buildMessageCreatedPayload(msg, "")
	payload["kind"] = kind
	if kind == model.InboxMessageDeleted {
		payload["delete_for_all"] = true
	}
	payload["user_id"] = msg.SenderID
	return s.PublishInboxChange(ctx, tx, payload)
}
