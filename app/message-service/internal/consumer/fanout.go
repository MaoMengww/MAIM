package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
)

// Fanout owns recipient resolution and the unread values clients observe.
// It never consults connection state: realtime routes these explicit recipients.
type Fanout struct {
	conversations *repo.ConversationRepo
	publisher     *delivery.Publisher
}

func NewFanout(conversations *repo.ConversationRepo, publisher *delivery.Publisher) *Fanout {
	return &Fanout{conversations: conversations, publisher: publisher}
}

func (f *Fanout) UserIDs(ctx context.Context, convID string) ([]string, error) {
	var ids []string
	err := f.conversations.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND member_type = ?", convID, model.MemberTypeUser).Pluck("user_id", &ids).Error
	return ids, err
}

func (f *Fanout) send(ctx context.Context, convID string, users []string, payload any, notification *delivery.Notification) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	return f.publisher.Publish(ctx, convID, delivery.Intent{UserIDs: users, Payload: raw, Notification: notification})
}

// ChangeCommitted persists every durable change through the user stream and
// emits the same ordered invalidation hint. New messages additionally retain
// the existing full realtime payload for immediate rendering.
func (f *Fanout) ChangeCommitted(ctx context.Context, evt event.InboxChangeEvent, raw []byte, users []string) error {
	title := "新消息"
	var names struct {
		SenderName string `json:"sender_name"`
	}
	if err := json.Unmarshal(raw, &names); err != nil {
		return err
	}
	if names.SenderName != "" {
		title = names.SenderName + " 发来消息"
	}
	convID := evt.ConvID
	preview := ""
	var counts map[string]int32
	var message json.RawMessage
	var hidden, hiddenReplies map[string]bool
	var tombstones []model.PersonalMessageDeletion
	if evt.Kind == model.InboxMessageNew && len(users) > 0 {
		if evt.MessageID == nil {
			return fmt.Errorf("new message requires a message identity")
		}
		hidden = make(map[string]bool)
		hiddenReplies = make(map[string]bool)
		messageIDs := []string{*evt.MessageID}
		if evt.ReplyToMsgID != nil {
			messageIDs = append(messageIDs, *evt.ReplyToMsgID)
		}
		if err := f.conversations.DB.WithContext(ctx).Where("user_id IN ? AND message_id IN ?", users, messageIDs).Find(&tombstones).Error; err != nil {
			return err
		}
		for _, d := range tombstones {
			if d.MessageID == *evt.MessageID {
				hidden[d.UserID] = true
			}
			if evt.ReplyToMsgID != nil && d.MessageID == *evt.ReplyToMsgID {
				hiddenReplies[d.UserID] = true
			}
		}
	}
	if evt.Kind == model.InboxMessageNew {
		var err error
		counts, err = f.conversations.UnreadCounts(ctx, evt.ConvID, users)
		if err != nil {
			return err
		}
		message = json.RawMessage(raw)
		preview = messagePreview(evt)
	}
	for _, uid := range users {
		var notification *delivery.Notification
		if evt.Kind == model.InboxMessageNew && !hidden[uid] && (evt.SenderID == nil || uid != *evt.SenderID) {
			notification = &delivery.Notification{Title: title, Body: preview,
				Data: map[string]string{"conv_id": convID, "preview": preview}}
		}
		if evt.Kind == model.InboxMessageNew && !hidden[uid] {
			visibleMessage := message
			if hiddenReplies[uid] {
				var value map[string]any
				decoder := json.NewDecoder(bytes.NewReader(message))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err != nil {
					return err
				}
				value["reply_to"] = map[string]any{"message_id": evt.ReplyToMsgID, "deleted": true}
				var err error
				visibleMessage, err = json.Marshal(value)
				if err != nil {
					return err
				}
			}
			if err := f.send(ctx, evt.ConvID, []string{uid}, map[string]any{
				"type": consts.EventMessageNew, "message": visibleMessage, "preview": preview,
				"conv_id": convID, "unread_count": counts[uid],
			}, notification); err != nil {
				return err
			}
		}
		if evt.Kind == model.InboxMessageDeleted {
			if err := f.send(ctx, evt.ConvID, []string{uid}, map[string]any{
				"type": "message.deleted", "conv_id": convID, "message_id": evt.MessageID,
				"message": map[string]any{"message_id": evt.MessageID, "conv_id": convID},
			}, nil); err != nil {
				return err
			}
		}
		if err := f.send(ctx, evt.ConvID, []string{uid}, map[string]any{
			"type": "inbox.changed", "conv_id": convID, "kind": evt.Kind,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fanout) Handle(ctx context.Context, topic string, raw []byte) error {
	var evt struct {
		ConvID      string `json:"conv_id"`
		UserID      string `json:"user_id"`
		LastReadSeq int64  `json:"last_read_seq"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	if err := identity.Validate(evt.ConvID); err != nil {
		return err
	}
	if err := identity.Validate(evt.UserID); err != nil {
		return err
	}
	if err := sequence.Validate(evt.LastReadSeq); err != nil {
		return err
	}
	users, err := f.UserIDs(ctx, evt.ConvID)
	if err != nil {
		return err
	}
	switch topic {
	case consts.KafkaTopicConversationReadUpdated:
		counts, err := f.conversations.UnreadCounts(ctx, evt.ConvID, users)
		if err != nil {
			return err
		}
		var others []string
		for _, uid := range users {
			if uid != evt.UserID {
				others = append(others, uid)
			}
		}
		if err := f.send(ctx, evt.ConvID, others, map[string]any{"type": consts.EventReadReceipt, "conv_id": evt.ConvID, "user_id": evt.UserID, "last_read_seq": evt.LastReadSeq}, nil); err != nil {
			return err
		}
		for _, uid := range users {
			if err := f.send(ctx, evt.ConvID, []string{uid}, map[string]any{"type": consts.EventUnreadCount, "conv_id": evt.ConvID, "unread_count": counts[uid]}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func messagePreview(evt event.InboxChangeEvent) string {
	text, _ := evt.Content["text"].(string)
	switch evt.MsgType {
	case 1:
		runes := []rune(text)
		if len(runes) > 20 {
			return string(runes[:20]) + "..."
		}
		if text != "" {
			return text
		}
	case 2:
		return "[图片]"
	case 3:
		return "[文件]"
	case 4:
		return "[视频]"
	case 5:
		return "[语音]"
	case 6:
		return "[位置]"
	case 7:
		if action, ok := evt.Content["action"].(string); ok && action != "" {
			return action
		}
		return "[系统消息]"
	case 9:
		if text != "" {
			return text
		}
		return "[机器人消息]"
	}
	return "[消息]"
}
