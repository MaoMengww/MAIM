package run

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type activityMessage struct {
	MessageID  string `json:"message_id"`
	SenderID   string `json:"sender_id"`
	SenderType string `json:"sender_type"`
	Text       string `json:"text"`
	Seq        int64  `json:"seq"`
}

// Observe delivers only the current human source selected by the platform
// consumer. It accepts no work, creates no Run and grants no result publication.
func (s *Service) Observe(ctx context.Context, input Input) error {
	if identity.Validate(input.BotID) != nil || identity.Validate(input.ConvID) != nil || identity.Validate(input.BindingID) != nil || identity.Validate(input.RequesterID) != nil || input.SourceMessageID == nil || identity.Validate(*input.SourceMessageID) != nil {
		return status.Error(codes.InvalidArgument, "invalid activity source")
	}
	bot, binding, err := s.svc.BotRepo.FindByIDWithConvBot(ctx, input.BotID, input.ConvID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.PermissionDenied, "activity binding is unavailable")
	}
	if err != nil {
		return err
	}
	if binding.ID != input.BindingID {
		return status.Error(codes.PermissionDenied, "activity binding is stale")
	}
	if bot.Status != "active" || bot.Type != "third_party" || !allowsMessageActivity(binding) {
		return nil
	}
	startSeq, subscribed := messageActivityStart(binding)
	if !subscribed {
		return nil
	}
	// The administrator's current binding subscription is the platform's
	// authority to select this one source, not a claim that a user cited it.
	// source clears human RPC metadata; RequesterID is only the recorded message
	// author's membership/visibility fact. No reply or history grant is read.
	source, err := s.source(ctx, input, *input.SourceMessageID, true)
	if err != nil {
		return err
	}
	if source.MessageId != *input.SourceMessageID || source.SenderType != "user" || source.GetSenderId() != input.RequesterID || source.GetText() == nil || source.Seq <= max(binding.StartSeq, startSeq) {
		return status.Error(codes.PermissionDenied, "activity source is not a current human message")
	}
	// Re-read the binding settings at each transport attempt. A removed or
	// replaced binding must never inherit observation from a former lifecycle.
	bot, binding, err = s.svc.BotRepo.FindByIDWithConvBot(ctx, input.BotID, input.ConvID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.PermissionDenied, "activity binding is unavailable")
	}
	if err != nil {
		return err
	}
	if binding.ID != input.BindingID || source.Seq <= binding.StartSeq {
		return status.Error(codes.PermissionDenied, "activity binding is stale")
	}
	if bot.Status != "active" || bot.Type != "third_party" || !allowsMessageActivity(binding) {
		return nil
	}
	startSeq, subscribed = messageActivityStart(binding)
	if !subscribed || source.Seq <= startSeq {
		return nil
	}
	payload, err := json.Marshal(struct {
		Type         string          `json:"type"`
		ActivityType string          `json:"activity_type"`
		BotID        string          `json:"bot_id"`
		ConvID       string          `json:"conv_id"`
		BindingID    string          `json:"binding_id"`
		Message      activityMessage `json:"message"`
	}{
		Type: "bot.activity", ActivityType: "message.created", BotID: bot.ID, ConvID: binding.ConvID, BindingID: binding.ID,
		Message: activityMessage{MessageID: source.MessageId, SenderID: source.GetSenderId(), SenderType: source.SenderType, Text: source.GetText().Text, Seq: source.Seq},
	})
	if err != nil {
		return err
	}
	return s.deliverPayload(ctx, bot, binding.ConvID, payload)
}

func allowsMessageActivity(binding *model.ConvBot) bool {
	// A missing, malformed or unknown declaration never widens access.
	types, ok := binding.BotSettings["activity_types"].([]any)
	return ok && slices.ContainsFunc(types, func(value any) bool { return value == "message.created" })
}

func messageActivityStart(binding *model.ConvBot) (int64, bool) {
	// The message domain stamps this safe-integer watermark when the
	// administrator changes settings. Missing legacy values fail closed.
	value, ok := binding.BotSettings["activity_start_seq"].(float64)
	if !ok || value < 0 || value > float64(sequence.Max) {
		return 0, false
	}
	start := int64(value)
	return start, value == float64(start)
}

// deliverPayload authenticates transport only. In particular, webhook success
// cannot acknowledge executor acceptance or authorize a Bot result.
func (s *Service) deliverPayload(ctx context.Context, bot *model.Bot, convID string, payload json.RawMessage) error {
	switch bot.ConnMode {
	case "ws":
		return s.svc.DeliveryPublisher.Publish(ctx, convID, delivery.Intent{BotIDs: []string{bot.ID}, Payload: payload})
	case "webhook":
		mac := hmac.New(sha256.New, []byte(bot.WebhookSecret))
		mac.Write(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, bot.CallbackURL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", consts.ContentTypeJSON)
		req.Header.Set(consts.HeaderAIMSignature, hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set(consts.HeaderAIMTimestamp, strconv.FormatInt(time.Now().Unix(), 10))
		client := &http.Client{Timeout: consts.WebhookCallbackTimeout * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return status.Error(codes.Unavailable, "executor webhook did not acknowledge transport")
		}
		return nil
	default:
		return status.Error(codes.FailedPrecondition, "unsupported executor route")
	}
}
