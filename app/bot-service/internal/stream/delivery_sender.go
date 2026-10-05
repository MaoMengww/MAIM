package stream

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/maomeng/aim/app/bot-service/internal/model"
)

type DeliveryClient interface {
	PublishToConversation(ctx context.Context, convID, botID int64, msg []byte) error
}

// DeliveryPusher publishes ephemeral chunks for current conversation members.
type DeliveryPusher struct {
	ctx          context.Context
	client       DeliveryClient
	convID       int64
	botID        int64
	replyToMsgID int64
	seq          int64
	streamID     string
}

func NewDeliveryPusher(ctx context.Context, client DeliveryClient, convID, botID, replyToMsgID int64) *DeliveryPusher {
	return &DeliveryPusher{ctx: ctx, client: client, convID: convID, botID: botID, replyToMsgID: replyToMsgID, streamID: uuid.NewString()}
}

func (w *DeliveryPusher) Send(chunk *model.StreamChunk) error {
	msg := map[string]any{"type": "bot.streaming." + chunk.Type, "stream_id": w.streamID, "bot_id": w.botID, "conv_id": w.convID, "content": chunk.Content, "seq": w.seq}
	if w.seq == 0 && w.replyToMsgID > 0 {
		msg["reply_to_msg_id"] = w.replyToMsgID
	}
	if chunk.Type == "tool_call" || chunk.Type == "tool_result" {
		msg["tool_name"] = chunk.ToolName
	}
	if chunk.Type == "done" {
		msg["message_id"] = chunk.MessageID
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal stream message: %w", err)
	}
	if err := w.client.PublishToConversation(w.ctx, w.convID, w.botID, raw); err != nil {
		return fmt.Errorf("stream to conversation: %w", err)
	}
	w.seq++
	return nil
}
