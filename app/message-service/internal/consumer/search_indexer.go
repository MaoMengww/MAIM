package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/elasticsearch"
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
	es         *elasticsearch.Client
	logger     logx.Logger
	maxRetries int
}

func NewSearchIndexer(es *elasticsearch.Client, logger logx.Logger, maxRetries int) *SearchIndexer {
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
	switch topic {
	case consts.KafkaTopicMessageCreated:
		return i.handleMessageCreated(ctx, data)
	case consts.KafkaTopicMessageEdited:
		return i.handleMessageEdited(ctx, data)
	case consts.KafkaTopicMessageRecalled:
		return i.handleMessageRecalled(ctx, data)
	case consts.KafkaTopicMessageDeleted:
		// 删除是 per-user 操作，不影响 ES 索引
		return nil
	default:
		return nil
	}
}

func (i *SearchIndexer) handleMessageCreated(ctx context.Context, data []byte) error {
	logger := i.logger.WithContext(ctx)

	var evt event.MessageCreatedEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}

	logger.Infof("search indexer: indexing message: msg_id=%d conv_id=%d", evt.MessageID, evt.ConvID)

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

func (i *SearchIndexer) handleMessageEdited(ctx context.Context, data []byte) error {
	logger := i.logger.WithContext(ctx)

	var evt event.MessageEditedEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}

	msgIDStr := fmt.Sprintf("%d", evt.MessageID)
	doc := ESMessageDoc{
		MessageID: msgIDStr,
		Content:   evt.NewContent,
		Text:      extractSearchText(evt.NewContent),
	}

	if err := i.es.Index(ctx, consts.ESIndexMessages, msgIDStr, doc); err != nil {
		return fmt.Errorf("es update failed: msg_id=%d err=%w", evt.MessageID, err)
	}

	logger.Infof("search indexer: updated message: msg_id=%d conv_id=%d", evt.MessageID, evt.ConvID)
	return nil
}

func (i *SearchIndexer) handleMessageRecalled(ctx context.Context, data []byte) error {
	logger := i.logger.WithContext(ctx)

	var evt event.MessageRecalledEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}

	msgIDStr := fmt.Sprintf("%d", evt.MessageID)
	if err := i.es.Delete(ctx, consts.ESIndexMessages, msgIDStr); err != nil {
		return fmt.Errorf("es delete failed: msg_id=%d err=%w", evt.MessageID, err)
	}

	logger.Infof("search indexer: deleted recalled message: msg_id=%d conv_id=%d", evt.MessageID, evt.ConvID)
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
