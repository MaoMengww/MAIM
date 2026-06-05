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

type ConvMemberResolver interface {
	GetConvMembers(ctx context.Context, convID int64) ([]int64, error)
}

type InboxWriter struct {
	inboxRepo   *repo.InboxRepo
	resolver    ConvMemberResolver
	logger      logx.Logger
	maxRetries  int
	dlqProducer *kafka.Producer
}

func NewInboxWriter(inboxRepo *repo.InboxRepo, resolver ConvMemberResolver, logger logx.Logger, maxRetries int, dlqProducer *kafka.Producer) *InboxWriter {
	return &InboxWriter{
		inboxRepo:   inboxRepo,
		resolver:    resolver,
		logger:      logger,
		maxRetries:  maxRetries,
		dlqProducer: dlqProducer,
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
			if msg.Topic != consts.KafkaTopicMessageCreated {
				session.MarkMessage(msg, "")
				continue
			}
			if err := w.handleMessageCreated(session.Context(), msg.Value); err != nil {
				if retryErr := w.retry(session.Context(), msg.Value); retryErr != nil {
					logger.Errorf("inbox writer failed after retries: key=%s err=%v", string(msg.Key), err)
				}
			}
			session.MarkMessage(msg, "")
		case <-session.Context().Done():
			return nil
		}
	}
}

func (w *InboxWriter) handleMessageCreated(ctx context.Context, data []byte) error {
	logger := w.logger.WithContext(ctx)

	var payload event.MessageCreatedEvent
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	logger.Infof("inbox writer: processing message: conv_id=%d seq=%d", payload.ConvID, payload.Seq)

	members, err := w.resolver.GetConvMembers(ctx, payload.ConvID)
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
			Seq:       payload.Seq,
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
	return nil
}

func (w *InboxWriter) retry(ctx context.Context, data []byte) error {
	for i := 0; i < w.maxRetries; i++ {
		time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
		if err := w.handleMessageCreated(ctx, data); err == nil {
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
