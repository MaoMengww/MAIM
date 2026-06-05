package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/app/signaling-service/internal/metrics"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
)

type DLQProducer interface {
	Send(ctx context.Context, topic string, key string, value []byte) error
}

type Consumer struct {
	fanout *Fanout
	dlq    DLQProducer
	logger logx.Logger
	topics map[string]func(context.Context, []byte) error
}

func NewConsumer(f *Fanout, dlq DLQProducer, logger logx.Logger) *Consumer {
	c := &Consumer{fanout: f, dlq: dlq, logger: logger, topics: make(map[string]func(context.Context, []byte) error)}
	c.topics[consts.KafkaTopicMessageCreated] = c.handleMessageCreated
	c.topics[consts.KafkaTopicMessageRecalled] = c.handleMessageRecalled
	c.topics[consts.KafkaTopicMessageEdited] = c.handleMessageEdited
	c.topics[consts.KafkaTopicMessageDeleted] = c.handleMessageDeleted
	c.topics[consts.KafkaTopicConversationReadUpdated] = c.handleConversationReadUpdated
	c.topics[consts.KafkaTopicConvBotAdded] = c.handleBotAdded
	c.topics[consts.KafkaTopicConvBotRemoved] = c.handleBotRemoved
	c.topics[consts.KafkaTopicConvMemberJoined] = c.handleMemberJoined
	c.topics[consts.KafkaTopicConvMemberLeft] = c.handleMemberLeft
	return c
}

func (c *Consumer) Topics() []string {
	keys := make([]string, 0, len(c.topics))
	for k := range c.topics {
		keys = append(keys, k)
	}
	return keys
}

func (c *Consumer) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (c *Consumer) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (c *Consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			handler, ok := c.topics[msg.Topic]
			if !ok {
				session.MarkMessage(msg, "")
				continue
			}
			ctx := kafka.ExtractTraceContext(session.Context(), msg.Headers)
			start := time.Now()
			err := c.withRetry(ctx, msg.Topic, msg.Value, handler)
			metrics.KafkaProcessingSeconds.Observe(time.Since(start).Seconds(), msg.Topic)
			metrics.KafkaMessagesTotal.Inc(msg.Topic)
			if err != nil {
				c.logger.WithContext(ctx).Errorf("kafka handler exhausted retries: topic=%s err=%v", msg.Topic, err)
				c.sendDLQ(ctx, msg.Topic, msg.Key, msg.Value)
			}
			session.MarkMessage(msg, "")
		case <-session.Context().Done():
			return nil
		}
	}
}

func (c *Consumer) withRetry(ctx context.Context, topic string, data []byte, handler func(context.Context, []byte) error) error {
	var lastErr error
	for i := 0; i < consts.RetryMaxAttempts; i++ {
		if err := handler(ctx, data); err != nil {
			lastErr = err
			backoff := time.Duration(consts.RetryBaseBackoffMs*(1<<i)) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("exhausted %d retries: %w", consts.RetryMaxAttempts, lastErr)
}

func (c *Consumer) sendDLQ(ctx context.Context, topic string, key []byte, value []byte) {
	if c.dlq == nil {
		return
	}
	sCtx, cancel := context.WithTimeout(ctx, time.Duration(consts.DLQTimeoutSec)*time.Second)
	defer cancel()
	c.dlq.Send(sCtx, topic+consts.DLQSuffix, string(key), value)
}

func (c *Consumer) handleMessageCreated(ctx context.Context, raw []byte) error {
	var evt struct {
		ConvID   int64 `json:"conv_id"`
		SenderID int64 `json:"sender_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushMessageNewWithUnread(ctx, evt.ConvID, evt.SenderID, raw)
}

func (c *Consumer) handleMessageRecalled(ctx context.Context, raw []byte) error {
	var evt struct {
		MessageID int64 `json:"message_id"`
		ConvID    int64 `json:"conv_id"`
		UserID    int64 `json:"user_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushMessageRecalled(ctx, evt.ConvID, evt.MessageID)
}

func (c *Consumer) handleMessageEdited(ctx context.Context, raw []byte) error {
	var evt struct {
		MessageID int64          `json:"message_id"`
		ConvID    int64          `json:"conv_id"`
		UserID    int64          `json:"user_id"`
		NewContent map[string]any `json:"new_content"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushMessageEdited(ctx, evt.ConvID, evt.UserID, raw)
}

func (c *Consumer) handleMessageDeleted(ctx context.Context, raw []byte) error {
	var evt struct {
		MessageID int64 `json:"message_id"`
		ConvID    int64 `json:"conv_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushMessageDeleted(ctx, evt.ConvID, 0, raw)
}

func (c *Consumer) handleConversationReadUpdated(ctx context.Context, raw []byte) error {
	var evt struct {
		ConvID      int64 `json:"conv_id"`
		UserID      int64 `json:"user_id"`
		LastReadSeq int64 `json:"last_read_seq"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	c.fanout.PushReadUpdated(ctx, evt.ConvID, evt.UserID, evt.LastReadSeq)
	return nil
}

func (c *Consumer) handleBotAdded(ctx context.Context, raw []byte) error {
	var evt struct{ ConvID int64 `json:"conv_id"`; BotID int64 `json:"bot_id"` }
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushBotAdded(ctx, evt.BotID, evt.ConvID)
}

func (c *Consumer) handleBotRemoved(ctx context.Context, raw []byte) error {
	var evt struct{ ConvID int64 `json:"conv_id"`; BotID int64 `json:"bot_id"` }
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushBotRemoved(ctx, evt.BotID, evt.ConvID)
}

func (c *Consumer) handleMemberJoined(ctx context.Context, raw []byte) error {
	var evt struct {
		ConvID  int64   `json:"conv_id"`
		UserIDs []int64 `json:"user_ids"`
		JoinedBy int64  `json:"joined_by"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushMemberJoined(ctx, evt.ConvID, evt.UserIDs)
}

func (c *Consumer) handleMemberLeft(ctx context.Context, raw []byte) error {
	var evt struct {
		ConvID    int64   `json:"conv_id"`
		UserIDs   []int64 `json:"user_ids"`
		RemovedBy int64   `json:"removed_by"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	return c.fanout.PushMemberLeft(ctx, evt.ConvID, evt.UserIDs)
}
