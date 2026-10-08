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

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/identity"
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
		ConvID    string  `json:"conv_id"`
		BotID     string  `json:"bot_id"`
		SenderID  *string `json:"sender_id"`
		Kind      string  `json:"kind"`
		CreatedAt int64   `json:"created_at"`
		Seq       int64   `json:"seq"`
		BindingID string  `json:"binding_id"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	if identity.Validate(evt.ConvID) != nil || (evt.BotID != "" && identity.Validate(evt.BotID) != nil) || (evt.SenderID != nil && identity.Validate(*evt.SenderID) != nil) || (evt.BindingID != "" && identity.Validate(evt.BindingID) != nil) {
		return fmt.Errorf("invalid third-party event identity")
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
		if topic != consts.KafkaTopicConvBotAdded && evt.Seq <= binding.StartSeq {
			continue
		}
		if topic == consts.KafkaTopicConvBotAdded && evt.BindingID != "" && evt.BindingID != binding.ID {
			continue
		}
		if topic == consts.KafkaTopicConvBotAdded && binding.BotID != evt.BotID {
			continue
		}
		if evt.SenderID != nil && binding.BotID == *evt.SenderID {
			continue
		}
		if err := h.deliver(ctx, topic, raw, &binding); err != nil {
			failures = append(failures, fmt.Errorf("bot %s: %w", binding.BotID, err))
		}
	}
	return errors.Join(failures...)
}

// deliver keeps the binding captured when routing the input across both transports.
func (h *ThirdPartyHandler) deliver(ctx context.Context, topic string, raw []byte, binding *model.ConvBot) error {
	bot, err := h.bots.GetBot(ctx, binding.BotID)
	if err != nil {
		return err
	}
	if bot.Type != consts.BotTypeThirdParty || bot.Status != consts.BotStatusActive {
		return nil
	}
	active, err := h.bots.HasActiveBinding(ctx, binding.BotID, binding.ConvID, binding.ID)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	payload, err := externalBotPayload(topic, raw, binding.BotID, binding.ConvID, binding.ID)
	if err != nil {
		return err
	}
	switch bot.ConnMode {
	case "ws":
		return h.publisher.Publish(ctx, binding.ConvID, delivery.Intent{BotIDs: []string{binding.BotID}, Payload: payload})
	case "webhook":
		return h.sendWebhook(ctx, binding, bot.CallbackURL, bot.WebhookSecret, payload)
	}
	return nil
}

func externalBotPayload(topic string, raw []byte, botID, convID, bindingID string) (json.RawMessage, error) {
	base := map[string]any{"type": topic, "conv_id": convID, "bot_id": botID, "binding_id": bindingID, "event": map[string]any{"ts": time.Now().Unix(), "version": "1.0"}}
	switch topic {
	case consts.KafkaTopicMessageCreated:
		var msg event.InboxChangeEvent
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, err
		}
		base["message"] = map[string]any{"message_id": msg.MessageID, "msg_type": msg.MsgType, "content": msg.Content, "seq": msg.Seq, "reply_to_msg_id": msg.ReplyToMsgID, "created_at": msg.CreatedAt}
		base["sender"] = map[string]any{"sender_id": msg.SenderID, "sender_type": msg.SenderType}
		if msg.SenderType == "user" {
			base["requester_id"] = msg.SenderID
		}
	case consts.KafkaTopicConvBotAdded:
		base["type"] = "bot.added_to_conv"
	default:
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var msg map[string]any
		if err := dec.Decode(&msg); err != nil {
			return nil, err
		}
		base["message"] = msg
	}
	payload, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	if err := model.ValidateEntityJSON(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (h *ThirdPartyHandler) sendWebhook(ctx context.Context, binding *model.ConvBot, url, secret string, payload []byte) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	var lastErr error
	for attempt := range consts.WebhookMaxRetries {
		active, err := h.bots.HasActiveBinding(ctx, binding.BotID, binding.ConvID, binding.ID)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("bot binding has been revoked")
		}
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
