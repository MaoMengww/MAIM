package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/app/message-service/internal/es"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
	"strings"
)

type ESMessageDoc struct {
	MessageID  string         `json:"message_id"`
	ConvID     int64          `json:"conv_id"`
	SenderID   int64          `json:"sender_id"`
	SenderType string         `json:"sender_type"`
	MsgType    int32          `json:"msg_type"`
	Content    map[string]any `json:"content"`
	Text       string         `json:"text"`
	CreatedAt  int64          `json:"created_at"`
}

type SearchIndexer struct {
	es         *es.Client
	logger     logx.Logger
	maxRetries int
}

func NewSearchIndexer(es *es.Client, logger logx.Logger, maxRetries int) *SearchIndexer {
	return &SearchIndexer{
		es:         es,
		logger:     logger,
		maxRetries: maxRetries,
	}
}

func (i *SearchIndexer) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (i *SearchIndexer) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (i *SearchIndexer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	logger := i.logger.WithContext(session.Context())
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if err := i.handleMessage(session.Context(), msg.Topic, msg.Value); err != nil {
				logger.Errorf("search indexer failed: topic=%s key=%s err=%v", msg.Topic, string(msg.Key), err)
			}
			session.MarkMessage(msg, "")
		case <-session.Context().Done():
			return nil
		}
	}
}

func (i *SearchIndexer) handleMessage(ctx context.Context, topic string, data []byte) error {
	if topic != consts.KafkaTopicMessageCreated {
		return nil
	}
	var evt event.InboxChangeEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}
	switch evt.Kind {
	case "message.new", "message.edited":
		return i.indexMessage(ctx, evt)
	case "message.recalled", "message.deleted":
		return i.es.Delete(ctx, consts.ESIndexMessages, fmt.Sprintf("%d", evt.MessageID))
	default:
		// Conversation/read changes are never message documents.
		return nil
	}
}

func (i *SearchIndexer) indexMessage(ctx context.Context, evt event.InboxChangeEvent) error {
	logger := i.logger.WithContext(ctx)

	msgIDStr := fmt.Sprintf("%d", evt.MessageID)
	doc := ESMessageDoc{
		MessageID:  msgIDStr,
		ConvID:     evt.ConvID,
		SenderID:   evt.SenderID,
		SenderType: evt.SenderType,
		MsgType:    evt.MsgType,
		Content:    evt.Content,
		Text:       extractSearchText(evt.Content),
		CreatedAt:  evt.CreatedAt,
	}

	if err := i.es.Index(ctx, consts.ESIndexMessages, msgIDStr, doc); err != nil {
		return fmt.Errorf("es index failed: msg_id=%d err=%w", evt.MessageID, err)
	}

	logger.Infof("search indexer: indexed message: msg_id=%d conv_id=%d", evt.MessageID, evt.ConvID)
	return nil
}

func extractSearchText(content map[string]any) string {
	if content == nil {
		return ""
	}
	fields := []string{"text", "detail", "name", "file_name", "address", "type", "data"}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		if v, ok := content[field].(string); ok && v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}
