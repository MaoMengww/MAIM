package client

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/graph"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/zeromicro/go-zero/zrpc"
)

// MessageClient wraps message-service gRPC client as a graph.MsgClient.
type MessageClient struct {
	cli msgpb.MessageServiceClient
}

func NewMessageClient(c zrpc.Client) *MessageClient {
	return &MessageClient{cli: msgpb.NewMessageServiceClient(c.Conn())}
}

// GetGrantedMessages reads only an explicitly granted history range as the bot.
func (c *MessageClient) GetGrantedMessages(ctx context.Context, botID, convID, bindingID, grantID string, limit int) ([]graph.Message, error) {
	resp, err := c.cli.GetMessages(serviceCallContext(ctx), &msgpb.GetMessagesReq{
		ConversationId: convID,
		BotId:          &botID,
		BindingId:      &bindingID,
		HistoryGrantId: &grantID,
		Pagination:     &msgpb.MessagePagination{Limit: int32(limit)},
	})
	if err != nil {
		return nil, err
	}
	msgs := make([]graph.Message, len(resp.Messages))
	for i, m := range resp.Messages {
		msgs[i] = graph.Message{
			MsgID:        m.MessageId,
			SenderID:     m.SenderId,
			SenderType:   m.SenderType,
			SenderName:   m.SenderName,
			SenderAvatar: m.SenderAvatar,
			BindingID:    m.BindingId,
			RequesterID:  m.RequesterId,
			Content:      extractText(m),
			MsgType:      int32(m.Type),
			Seq:          m.Seq,
			CreatedAt:    m.CreatedAt,
		}
	}
	return msgs, nil
}

func extractText(m *msgpb.Message) string {
	if m == nil {
		return ""
	}
	if text := m.GetText(); text != nil {
		return text.Text
	}
	if file := m.GetFile(); file != nil {
		return file.Name
	}
	if result := m.GetResult(); result != nil {
		return result.Data
	}
	if custom := m.GetCustom(); custom != nil {
		return custom.Data
	}
	return ""
}

// SendBotReply never resolves a new binding: the caller supplies its captured identity.
func (c *MessageClient) SendBotReply(ctx context.Context, req *msgpb.SendBotReplyReq) (string, error) {
	resp, err := c.cli.SendBotReply(serviceCallContext(ctx), req)
	if err != nil {
		return "", err
	}
	return resp.MessageId, nil
}
