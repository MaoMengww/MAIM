package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
)

// MessageCreatedEvent mirrors the event payload from message-service.
type MessageCreatedEvent struct {
	MessageID   int64  `json:"message_id"`
	ConvID      int64  `json:"conv_id"`
	Seq         int64  `json:"seq"`
	PreviewText string `json:"preview_text"`
}

// NewMessageHandler creates a handler for message.created events.
// The handler is called by the Kafka consumer goroutine after parse + commit.
func NewMessageHandler(ctx *svc.ServiceContext) func(context.Context, []byte, []byte) error {
	logger := ctx.Logger
	return func(cctx context.Context, key, value []byte) error {
		var evt MessageCreatedEvent
		if err := json.Unmarshal(value, &evt); err != nil {
			logger.Errorf("failed to unmarshal message.created event: %v", err)
			return nil
		}
		if evt.ConvID == 0 || evt.MessageID == 0 {
			return nil
		}

		if err := ctx.Repo.UpdateConversationLastMessage(cctx, evt.ConvID, evt.MessageID, evt.PreviewText, evt.Seq); err != nil {
			logger.Errorf("failed to update conv last_message: conv=%d msg=%d err=%v", evt.ConvID, evt.MessageID, err)
			return fmt.Errorf("update conv last_message failed: %w", err)
		}
		return nil
	}
}
