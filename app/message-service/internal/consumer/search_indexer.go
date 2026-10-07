package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/app/message-service/internal/es"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
)

type ESMessageDoc struct {
	MessageID  string         `json:"message_id"`
	ConvID     string         `json:"conv_id"`
	SenderID   *string        `json:"sender_id,omitempty"`
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
	if evt.MessageID == nil {
		switch evt.Kind {
		case "message.new", "message.edited", "message.deleted", "message.recalled":
			return fmt.Errorf("message change requires a message identity")
		}
	}
	switch evt.Kind {
	case "message.new", "message.edited":
		return i.indexMessage(ctx, evt)
	case "message.deleted":
		// Personal tombstones never remove another member's search document.
		if !evt.DeleteForAll {
			return nil
		}
		return i.es.Delete(ctx, consts.ESIndexMessages, *evt.MessageID)
	case "message.recalled":
		return i.es.Delete(ctx, consts.ESIndexMessages, *evt.MessageID)
	default:
		// Conversation/read changes are never message documents.
		return nil
	}
}

func (i *SearchIndexer) indexMessage(ctx context.Context, evt event.InboxChangeEvent) error {
	logger := i.logger.WithContext(ctx)

	msgIDStr := *evt.MessageID
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
		return fmt.Errorf("es index failed: msg_id=%s err=%w", msgIDStr, err)
	}

	logger.Infof("search indexer: indexed message: msg_id=%s conv_id=%s", msgIDStr, evt.ConvID)
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
