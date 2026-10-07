package client

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/bot-service/internal/graph"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc/metadata"
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
func (c *MessageClient) GetRecentMessages(ctx context.Context, convID, userID string, limit int) ([]graph.Message, error) {
	resp, err := c.cli.GetMessages(messageUserContext(ctx, userID), &msgpb.GetMessagesReq{
		ConversationId: convID,
		UserId:         userID,
		Pagination: &msgpb.MessagePagination{
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
			BotID:      messageBotID(m),
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
func (c *MessageClient) GetAllMessages(ctx context.Context, convID, userID string, maxCount int) ([]graph.Message, error) {
	if maxCount <= 0 || maxCount > 1000 {
		maxCount = 1000
	}
	var all []graph.Message
	var cursor int64
	for len(all) < maxCount {
		limit := min(100, maxCount-len(all))
		resp, err := c.cli.GetMessages(messageUserContext(ctx, userID), &msgpb.GetMessagesReq{
			ConversationId: convID,
			UserId:         userID,
			Pagination: &msgpb.MessagePagination{
				Limit:  int32(limit),
				Cursor: cursor,
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
		for _, m := range resp.Messages {
			all = append(all, graph.Message{
				MsgID:      m.MessageId,
				SenderID:   m.FromUserId,
				SenderName: senderName(m),
				BotID:      messageBotID(m),
				Content:    extractText(m),
				MsgType:    int32(m.Type),
				Seq:        m.Seq,
				CreatedAt:  m.CreatedAt,
			})
		}
		if resp.Pagination == nil || !resp.Pagination.HasMore {
			break
		}
		next := resp.Pagination.NextCursor
		if next <= 0 || (cursor > 0 && next >= cursor) {
			return nil, fmt.Errorf("message history cursor did not advance")
		}
		cursor = next
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

func messageBotID(m *msgpb.Message) string {
	if bot := m.GetBot(); bot != nil {
		return bot.BotId
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
func (c *MessageClient) SendBotReply(ctx context.Context, botID, convID string, text string, replyTo *string, rawPayload ...string) (string, error) {
	rp := ""
	if len(rawPayload) > 0 {
		rp = rawPayload[0]
	}
	resp, err := c.cli.SendBotReply(serviceCallContext(ctx), &msgpb.SendBotReplyReq{
		BotId:          botID,
		ConversationId: convID,
		Text:           text,
		ReplyToId:      replyTo,
		RawPayload:     rp,
	})
	if err != nil {
		return "", err
	}
	return resp.MessageId, nil
}

func messageUserContext(ctx context.Context, userID string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("user-id", userID)
	return metadata.NewOutgoingContext(ctx, md)
}
