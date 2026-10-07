package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
			for {
				if err := w.handle(ctx, msg.Topic, msg.Value); err == nil {
					break
				}
				if retryErr := w.retry(ctx, msg.Topic, msg.Value); retryErr == nil {
					break
				}
				logger.Errorf("inbox writer blocked on failed event: key=%s", string(msg.Key))
				select {
				case <-session.Context().Done():
					return nil
				case <-time.After(time.Second):
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

	var payload event.InboxChangeEvent
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.PublicationSequence == 0 {
		return errors.New("inbox change requires a publication sequence")
	}
	var recipients []string
	switch payload.Kind {
	case model.InboxConversationRemoved:
		// A removed user's final change must survive loss of membership.
		recipients = payload.RecipientIDs
	case model.InboxReadUpdated:
		if payload.UserID == nil {
			return errors.New("read change requires a user")
		}
		members, err := w.fanout.UserIDs(ctx, payload.ConvID)
		if err != nil {
			return err
		}
		if slices.Contains(members, *payload.UserID) {
			recipients = []string{*payload.UserID}
		}
	case model.InboxConversationUpsert, model.InboxMessageNew, model.InboxMessageEdited, model.InboxMessageRecalled, model.InboxMessageDeleted:
		members, err := w.fanout.UserIDs(ctx, payload.ConvID)
		if err != nil {
			return err
		}
		for _, uid := range payload.RecipientIDs {
			if slices.Contains(members, uid) {
				recipients = append(recipients, uid)
			}
		}
	default:
		return errors.New("unknown inbox change kind")
	}
	if payload.Kind == model.InboxMessageNew || payload.Kind == model.InboxMessageEdited || payload.Kind == model.InboxMessageRecalled || payload.Kind == model.InboxMessageDeleted {
		if payload.MessageID == nil {
			return errors.New("message change requires a message identity")
		}
	}
	now := time.Now()
	inboxes := make([]model.UserInbox, 0, len(recipients))
	for _, uid := range recipients {
		inboxes = append(inboxes, model.UserInbox{UserID: uid, ConvID: payload.ConvID,
			MessageID: payload.MessageID, Kind: payload.Kind, ChangeID: payload.ChangeID,
			LastReadSeq: payload.LastReadSeq, CreatedAt: now})
	}
	if err := w.inboxRepo.BatchInsert(ctx, inboxes); err != nil {
		logger.Errorf("batch insert inbox failed: %v", err)
		return err
	}
	// No live change is published before the durable stream transaction commits.
	if err := w.fanout.ChangeCommitted(ctx, payload, data, recipients); err != nil {
		return err
	}
	if payload.Kind == model.InboxReadUpdated {
		return w.fanout.Handle(ctx, consts.KafkaTopicConversationReadUpdated, data)
	}
	return nil
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
