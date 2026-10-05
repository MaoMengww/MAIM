package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/event"
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

func (f *Fanout) UserIDs(ctx context.Context, convID int64) ([]int64, error) {
	var ids []int64
	err := f.conversations.DB.WithContext(ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND member_type = ?", convID, model.MemberTypeUser).Pluck("user_id", &ids).Error
	return ids, err
}

func (f *Fanout) send(ctx context.Context, convID int64, users []int64, payload any, notification *delivery.Notification) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	return f.publisher.Publish(ctx, convID, delivery.Intent{UserIDs: users, Payload: raw, Notification: notification})
}

func (f *Fanout) MessageCreated(ctx context.Context, evt event.MessageCreatedEvent, raw []byte, users []int64) error {
	counts, err := f.conversations.UnreadCounts(ctx, evt.ConvID, users)
	if err != nil {
		return err
	}
	message, err := clientMessage(raw)
	if err != nil {
		return err
	}
	preview := messagePreview(evt)
	convID := strconv.FormatInt(evt.ConvID, 10)
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
	for _, uid := range users {
		envelope := map[string]any{"type": consts.EventMessageNew, "message": message, "preview": preview, "conv_id": convID, "unread_count": counts[uid]}
		var notification *delivery.Notification
		if evt.SenderType == "bot" || uid != evt.SenderID {
			notification = &delivery.Notification{Title: title, Body: preview, Data: map[string]string{"conv_id": convID, "preview": preview}}
		}
		if err := f.send(ctx, evt.ConvID, []int64{uid}, envelope, notification); err != nil {
			return err
		}
	}
	return nil
}

func (f *Fanout) Handle(ctx context.Context, topic string, raw []byte) error {
	var evt struct {
		ConvID       int64 `json:"conv_id"`
		MessageID    int64 `json:"message_id"`
		UserID       int64 `json:"user_id"`
		LastReadSeq  int64 `json:"last_read_seq"`
		DeleteForAll bool  `json:"delete_for_all"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	users, err := f.UserIDs(ctx, evt.ConvID)
	if err != nil {
		return err
	}
	switch topic {
	case consts.KafkaTopicMessageRecalled:
		return f.send(ctx, evt.ConvID, users, map[string]any{"type": consts.EventMessageRecalled, "message_id": strconv.FormatInt(evt.MessageID, 10), "conv_id": strconv.FormatInt(evt.ConvID, 10)}, nil)
	case consts.KafkaTopicMessageEdited:
		msg, err := clientMessage(raw)
		if err != nil {
			return err
		}
		return f.send(ctx, evt.ConvID, users, map[string]any{"type": consts.EventMessageEdited, "message": msg}, nil)
	case consts.KafkaTopicMessageDeleted:
		msg, err := clientMessage(raw)
		if err != nil {
			return err
		}
		if evt.DeleteForAll {
			return f.send(ctx, evt.ConvID, users, map[string]any{"type": "message.deleted", "message": msg}, nil)
		}
		// A personal deletion only synchronizes the actor's devices.
		for _, uid := range users {
			if uid == evt.UserID {
				return f.send(ctx, evt.ConvID, []int64{uid}, map[string]any{"type": "message.deleted", "message": msg}, nil)
			}
		}
	case consts.KafkaTopicConversationReadUpdated:
		counts, err := f.conversations.UnreadCounts(ctx, evt.ConvID, users)
		if err != nil {
			return err
		}
		var others []int64
		for _, uid := range users {
			if uid != evt.UserID {
				others = append(others, uid)
			}
		}
		if err := f.send(ctx, evt.ConvID, others, map[string]any{"type": consts.EventReadReceipt, "conv_id": evt.ConvID, "user_id": evt.UserID, "last_read_seq": evt.LastReadSeq}, nil); err != nil {
			return err
		}
		for _, uid := range users {
			if err := f.send(ctx, evt.ConvID, []int64{uid}, map[string]any{"type": consts.EventUnreadCount, "conv_id": evt.ConvID, "unread_count": counts[uid]}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func clientMessage(raw []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	stringifyIDs(value)
	return json.Marshal(value)
}

func stringifyIDs(value any) {
	switch object := value.(type) {
	case map[string]any:
		for key, value := range object {
			if strings.HasSuffix(key, "_id") {
				if number, ok := value.(json.Number); ok {
					object[key] = number.String()
				}
			} else if strings.HasSuffix(key, "_ids") {
				if values, ok := value.([]any); ok {
					for i, v := range values {
						if number, ok := v.(json.Number); ok {
							values[i] = number.String()
						}
					}
				}
			} else {
				stringifyIDs(value)
			}
		}
	case []any:
		for _, v := range object {
			stringifyIDs(v)
		}
	}
}

func messagePreview(evt event.MessageCreatedEvent) string {
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
