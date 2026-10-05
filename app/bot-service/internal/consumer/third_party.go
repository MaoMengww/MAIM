package consumer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/IBM/sarama"
	"net/http"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/kafka"
	"github.com/maomeng/aim/pkg/logx"
)

// ThirdPartyHandler owns both third-party transports and reads callback secrets
// only from the Bot domain. Its Kafka group is separate from the AI runtime.
type ThirdPartyHandler struct {
	bots      *repo.BotRepo
	bindings  *repo.ConvBotRepo
	publisher *delivery.Publisher
	http      *http.Client
	logger    logx.Logger
}

func NewThirdPartyHandler(bots *repo.BotRepo, bindings *repo.ConvBotRepo, publisher *delivery.Publisher, logger logx.Logger) *ThirdPartyHandler {
	return &ThirdPartyHandler{bots: bots, bindings: bindings, publisher: publisher, logger: logger, http: &http.Client{Timeout: consts.WebhookCallbackTimeout * time.Second}}
}

func (h *ThirdPartyHandler) Handle(ctx context.Context, topic string, raw []byte) error {
	var evt struct {
		ConvID   int64  `json:"conv_id"`
		BotID    int64  `json:"bot_id"`
		SenderID int64  `json:"sender_id"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	if topic == consts.KafkaTopicMessageCreated {
		switch evt.Kind {
		case "message.new":
		case "message.edited":
			topic = consts.KafkaTopicMessageEdited
		case "message.recalled":
			topic = consts.KafkaTopicMessageRecalled
		default:
			return nil
		}
	}
	bindings, err := h.bindings.FindByConv(ctx, evt.ConvID)
	if err != nil {
		return err
	}
	// One unhealthy bot must not starve the rest of the conversation's bots.
	var failures []error
	for _, binding := range bindings {
		if topic == consts.KafkaTopicConvBotAdded && binding.BotID != evt.BotID {
			continue
		}
		if binding.BotID == evt.SenderID {
			continue
		}
		if err := h.deliver(ctx, topic, raw, evt.ConvID, binding.BotID); err != nil {
			failures = append(failures, fmt.Errorf("bot %d: %w", binding.BotID, err))
		}
	}
	return errors.Join(failures...)
}

// deliver routes one event to one bound bot, resolving its transport each time.
func (h *ThirdPartyHandler) deliver(ctx context.Context, topic string, raw []byte, convID, botID int64) error {
	bot, _, err := h.bots.FindByIDWithConvBot(ctx, botID, convID)
	if err != nil {
		return err
	}
	if bot.Type != consts.BotTypeThirdParty || bot.Status != consts.BotStatusActive {
		return nil
	}
	payload, err := externalBotPayload(topic, raw, botID, convID)
	if err != nil {
		return err
	}
	switch bot.ConnMode {
	case "ws":
		return h.publisher.Publish(ctx, convID, delivery.Intent{BotIDs: []int64{botID}, Payload: payload})
	case "webhook":
		return h.sendWebhook(ctx, bot.CallbackURL, bot.WebhookSecret, payload)
	}
	return nil
}

func externalBotPayload(topic string, raw []byte, botID, convID int64) (json.RawMessage, error) {
	base := map[string]any{"type": topic, "conv_id": strconv.FormatInt(convID, 10), "bot_id": strconv.FormatInt(botID, 10), "event": map[string]any{"ts": time.Now().Unix(), "version": "1.0"}}
	switch topic {
	case consts.KafkaTopicMessageCreated:
		var msg event.InboxChangeEvent
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}
		base["message"] = map[string]any{"message_id": strconv.FormatInt(msg.MessageID, 10), "msg_type": msg.MsgType, "content": msg.Content, "seq": strconv.FormatInt(msg.Seq, 10), "reply_to_msg_id": strconv.FormatInt(msg.ReplyToMsgID, 10), "created_at": strconv.FormatInt(msg.CreatedAt, 10)}
		base["sender"] = map[string]any{"user_id": strconv.FormatInt(msg.SenderID, 10), "sender_type": msg.SenderType}
	case consts.KafkaTopicConvBotAdded:
		base["type"] = "bot.added_to_conv"
	default:
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var msg map[string]any
		if err := dec.Decode(&msg); err != nil {
			return nil, err
		}
		for _, key := range []string{"message_id", "conv_id", "user_id"} {
			if number, ok := msg[key].(json.Number); ok {
				msg[key] = number.String()
			}
		}
		base["message"] = msg
	}
	return json.Marshal(base)
}

func (h *ThirdPartyHandler) sendWebhook(ctx context.Context, url, secret string, payload []byte) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	var lastErr error
	for attempt := range consts.WebhookMaxRetries {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("webhook request: %w", err)
		}
		req.Header.Set("Content-Type", consts.ContentTypeJSON)
		req.Header.Set(consts.HeaderAIMSignature, signature)
		req.Header.Set(consts.HeaderAIMTimestamp, timestamp)
		resp, err := h.http.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			err = fmt.Errorf("webhook returned %d", resp.StatusCode)
		}
		lastErr = err
		if attempt+1 < consts.WebhookMaxRetries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second * time.Duration(1<<attempt)):
			}
		}
	}
	return lastErr
}

func (h *ThirdPartyHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *ThirdPartyHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h *ThirdPartyHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		ctx := kafka.ExtractTraceContext(session.Context(), msg.Headers)
		if err := h.Handle(ctx, msg.Topic, msg.Value); err != nil {
			h.logger.WithContext(ctx).Errorf("third-party delivery failed: topic=%s err=%v", msg.Topic, err)
		}
		session.MarkMessage(msg, "")
	}
	return nil
}
