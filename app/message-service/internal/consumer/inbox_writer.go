package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
)

var errMaxRetries = errors.New("max retries exceeded")

type InboxWriter struct {
	inboxRepo   *repo.InboxRepo
	logger      logx.Logger
	maxRetries  int
	dlqProducer *kafka.Producer
	fanout      *Fanout
}

func NewInboxWriter(inboxRepo *repo.InboxRepo, fanout *Fanout, logger logx.Logger, maxRetries int, dlqProducer *kafka.Producer) *InboxWriter {
	return &InboxWriter{
		inboxRepo:   inboxRepo,
		logger:      logger,
		maxRetries:  maxRetries,
		dlqProducer: dlqProducer,
		fanout:      fanout,
	}
}

func (w *InboxWriter) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (w *InboxWriter) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (w *InboxWriter) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	logger := w.logger.WithContext(session.Context())
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			ctx := kafka.ExtractTraceContext(session.Context(), msg.Headers)
			if err := w.handle(ctx, msg.Topic, msg.Value); err != nil {
				if retryErr := w.retry(ctx, msg.Topic, msg.Value); retryErr != nil {
					logger.Errorf("inbox writer failed after retries: key=%s err=%v", string(msg.Key), err)
				}
			}
			session.MarkMessage(msg, "")
		case <-session.Context().Done():
			return nil
		}
	}
}

func (w *InboxWriter) handle(ctx context.Context, topic string, data []byte) error {
	if topic == consts.KafkaTopicMessageCreated {
		return w.handleMessageCreated(ctx, data)
	}
	return w.fanout.Handle(ctx, topic, data)
}

func (w *InboxWriter) handleMessageCreated(ctx context.Context, data []byte) error {
	logger := w.logger.WithContext(ctx)

	var payload event.MessageCreatedEvent
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	// Broadcasts still use this topic until issue11; they have no conversation
	// and must never enter the user synchronization stream.
	if payload.ConvID == 0 {
		var broadcast event.BroadcastCreatedEvent
		if err := json.Unmarshal(data, &broadcast); err != nil {
			return err
		}
		if broadcast.BroadcastID > 0 {
			return nil
		}
		return errors.New("new message requires a conversation")
	}

	logger.Infof("inbox writer: processing message: conv_id=%d seq=%d", payload.ConvID, payload.Seq)

	members, err := w.fanout.UserIDs(ctx, payload.ConvID)
	if err != nil {
		logger.Errorf("get conv members failed: conv=%d err=%v", payload.ConvID, err)
		return err
	}

	now := time.Now()
	inboxes := make([]model.UserInbox, 0, len(members))

	for _, uid := range members {
		inboxes = append(inboxes, model.UserInbox{
			UserID:    uid,
			ConvID:    payload.ConvID,
			MessageID: payload.MessageID,
			Kind:      model.InboxMessageNew,
			CreatedAt: now,
		})
	}

	if len(inboxes) == 0 {
		return nil
	}

	if err := w.inboxRepo.BatchInsert(ctx, inboxes); err != nil {
		logger.Errorf("batch insert inbox failed: %v", err)
		return err
	}

	logger.Infof("inbox writer: wrote %d inbox entries: conv_id=%d seq=%d", len(inboxes), payload.ConvID, payload.Seq)
	return w.fanout.MessageCreated(ctx, payload, data, members)
}

func (w *InboxWriter) retry(ctx context.Context, topic string, data []byte) error {
	for i := range w.maxRetries {
		time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
		if err := w.handle(ctx, topic, data); err == nil {
			return nil
		}
	}
	if w.dlqProducer != nil {
		if err := w.dlqProducer.Send(ctx, "0", data); err != nil {
			w.logger.WithContext(ctx).Errorf("send to DLQ failed: %v", err)
		} else {
			w.logger.WithContext(ctx).Infof("sent to DLQ: %s", consts.KafkaTopicMessageCreatedDLQ)
		}
	}
	return errMaxRetries
}
