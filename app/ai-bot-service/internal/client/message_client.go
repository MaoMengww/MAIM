package client

import (
	"context"

	"github.com/maomeng/aim/app/ai-bot-service/internal/graph"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/zrpc"
)

// MessageClient wraps message-service gRPC client as a graph.MsgClient.
type MessageClient struct {
	cli msgpb.MessageServiceClient
}

// NewMessageClient creates a new message-service client wrapper.
func NewMessageClient(c zrpc.Client) *MessageClient {
	return &MessageClient{
		cli: msgpb.NewMessageServiceClient(c.Conn()),
	}
}

// GetRecentMessages fetches recent messages from a conversation.
func (c *MessageClient) GetRecentMessages(ctx context.Context, convID, userID int64, limit int) ([]graph.Message, error) {
	resp, err := c.cli.GetMessages(ctx, &msgpb.GetMessagesReq{
		ConversationId: convID,
		UserId:         userID,
		Pagination: &common.CursorPagination{
			Limit: int32(limit),
		},
		FilterTypes: []msgpb.MessageType{
			msgpb.MessageType_MESSAGE_TYPE_TEXT,
			msgpb.MessageType_MESSAGE_TYPE_IMAGE,
			msgpb.MessageType_MESSAGE_TYPE_AUDIO,
			msgpb.MessageType_MESSAGE_TYPE_BOT,
		},
	})
	if err != nil {
		return nil, err
	}
	msgs := make([]graph.Message, len(resp.Messages))
	for i, m := range resp.Messages {
		content := extractText(m)
		msgs[i] = graph.Message{
			MsgID:      m.MessageId,
			SenderID:   m.FromUserId,
			SenderName: senderName(m),
			Content:    content,
			MsgType:    int32(m.Type),
			Seq:        m.Seq,
			CreatedAt:  m.CreatedAt,
		}
	}
	return msgs, nil
}

// GetAllMessages fetches up to maxCount messages for a conversation.
// Messages are returned in reverse chronological order (newest first).
func (c *MessageClient) GetAllMessages(ctx context.Context, convID, userID int64, maxCount int) ([]graph.Message, error) {
	if maxCount <= 0 || maxCount > 1000 {
		maxCount = 1000
	}
	var all []graph.Message
	limit := 100
	for len(all) < maxCount {
		remaining := maxCount - len(all)
		if limit > remaining {
			limit = remaining
		}
		resp, err := c.cli.GetMessages(ctx, &msgpb.GetMessagesReq{
			ConversationId: convID,
			UserId:         userID,
			Pagination: &common.CursorPagination{
				Limit: int32(limit),
			},
			FilterTypes: []msgpb.MessageType{
				msgpb.MessageType_MESSAGE_TYPE_TEXT,
				msgpb.MessageType_MESSAGE_TYPE_IMAGE,
				msgpb.MessageType_MESSAGE_TYPE_AUDIO,
				msgpb.MessageType_MESSAGE_TYPE_BOT,
			},
		})
		if err != nil {
			return nil, err
		}
		if len(resp.Messages) == 0 {
			break
		}
		// Deduplicate by MsgID
		existing := make(map[int64]bool)
		for _, m := range all {
			existing[m.MsgID] = true
		}
		for _, m := range resp.Messages {
			msgID := m.MessageId
			if !existing[msgID] {
				all = append(all, graph.Message{
					MsgID:      m.MessageId,
					SenderID:   m.FromUserId,
					SenderName: senderName(m),
					Content:    extractText(m),
					MsgType:    int32(m.Type),
					Seq:        m.Seq,
					CreatedAt:  m.CreatedAt,
				})
				existing[msgID] = true
			}
		}
		// If we got fewer messages than requested, we've reached the end
		if len(resp.Messages) < limit {
			break
		}
	}
	if len(all) > maxCount {
		all = all[:maxCount]
	}
	return all, nil
}

// extractText pulls plain text from a message's oneof content.
func senderName(m *msgpb.Message) string {
	if m == nil {
		return ""
	}
	if c := m.GetBot(); c != nil {
		return c.GetBotName()
	}
	return ""
}

func extractText(m *msgpb.Message) string {
	if m == nil {
		return ""
	}
	switch c := m.Content.(type) {
	case *msgpb.Message_Text:
		if c.Text != nil {
			return c.Text.GetText()
		}
	case *msgpb.Message_Bot:
		if c.Bot != nil {
			return c.Bot.GetText()
		}
	}
	return ""
}

// SendBotReply sends a bot reply message.
func (c *MessageClient) SendBotReply(ctx context.Context, botID, convID int64, text string, replyTo int64, rawPayload ...string) (int64, error) {
	rp := ""
	if len(rawPayload) > 0 {
		rp = rawPayload[0]
	}
	resp, err := c.cli.SendBotReply(ctx, &msgpb.SendBotReplyReq{
		BotId:          botID,
		ConversationId: convID,
		Text:           text,
		ReplyToId:      &replyTo,
		RawPayload:     rp,
	})
	if err != nil {
		return 0, err
	}
	return resp.MessageId, nil
}
