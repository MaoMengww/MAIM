package stream

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
)

// WsGatewayClient is the gRPC client for sending WS messages.
type WsGatewayClient interface {
	PushToConv(ctx context.Context, convID int64, msg []byte) error
}

// WSPusher sends streaming chunks to ws-gateway for group chat streaming.
type WSPusher struct {
	client       WsGatewayClient
	convID       int64
	botID        int64
	replyToMsgID int64
	seq          int64
}

// NewWSPusher creates a new WSPusher.
func NewWSPusher(client WsGatewayClient, convID, botID, replyToMsgID int64) *WSPusher {
	return &WSPusher{
		client:       client,
		convID:       convID,
		botID:        botID,
		replyToMsgID: replyToMsgID,
	}
}

// Send pushes a chunk to ws-gateway for delivery to online conversation members.
func (w *WSPusher) Send(chunk *model.StreamChunk) error {
	wsMsg := map[string]any{
		"type":    "bot.streaming." + chunk.Type,
		"bot_id":  w.botID,
		"conv_id": w.convID,
		"content": chunk.Content,
		"seq":     w.seq,
	}
	// Include reply info on the first chunk so frontend can render reply bar immediately
	if w.seq == 0 && w.replyToMsgID > 0 {
		wsMsg["reply_to_msg_id"] = w.replyToMsgID
	}
	if chunk.Type == "tool_call" || chunk.Type == "tool_result" {
		wsMsg["tool_name"] = chunk.ToolName
	}
	if chunk.Type == "done" {
		wsMsg["message_id"] = chunk.MessageID
	}

	b, err := json.Marshal(wsMsg)
	if err != nil {
		return fmt.Errorf("marshal ws message: %w", err)
	}

	if err := w.client.PushToConv(context.Background(), w.convID, b); err != nil {
		return fmt.Errorf("stream to conv: %w", err)
	}

	w.seq++
	return nil
}
