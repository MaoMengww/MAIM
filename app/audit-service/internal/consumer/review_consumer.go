package consumer

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/audit-service/internal/svc"

	"github.com/IBM/sarama"
)

type ReviewConsumer struct {
	svcCtx *svc.ServiceContext
}

func NewReviewConsumer(svcCtx *svc.ServiceContext) *ReviewConsumer {
	return &ReviewConsumer{svcCtx: svcCtx}
}

type MessageCreatedEvent struct {
	MessageID int64  `json:"message_id"`
	ConvID    int64  `json:"conv_id"`
	SenderID  int64  `json:"sender_id"`
	MsgType   int32  `json:"msg_type"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}

func (c *ReviewConsumer) Consume(ctx context.Context, msg *sarama.ConsumerMessage) error {
	logger := c.svcCtx.Logger.WithContext(ctx)
	logger.Infof("ReviewConsumer received message: topic=%s partition=%d offset=%d", msg.Topic, msg.Partition, msg.Offset)

	var event MessageCreatedEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Errorf("ReviewConsumer unmarshal failed: err=%v", err)
		return nil
	}

	status := c.mockReview(event)

	c.svcCtx.AuditRepo.UpdateReviewResult(ctx,
		"msg:"+string(msg.Key),
		status,
		map[string]any{
			"message_id": event.MessageID,
			"conv_id":    event.ConvID,
			"sender_id":  event.SenderID,
			"msg_type":   event.MsgType,
		},
	)

	logger.Infof("ReviewConsumer review completed: message_id=%d status=%d", event.MessageID, status)
	return nil
}

func (c *ReviewConsumer) mockReview(event MessageCreatedEvent) int32 {
	if event.Content == "" || len(event.Content) < 5 {
		return 1
	}
	return 1
}
