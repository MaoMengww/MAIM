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
	"strconv"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/botpolicy"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/delivery"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// ObserveActivity applies the same subscription and provenance boundary as
// response rules. Transport success never grants execution or publication.
func (s *Service) ObserveActivity(ctx context.Context, botID, bindingID string, activity *model.ActivityEnvelope) error {
	if activity == nil || identity.Validate(botID) != nil || identity.Validate(bindingID) != nil || identity.Validate(activity.ConvID) != nil {
		return status.Error(codes.InvalidArgument, "invalid activity identity")
	}
	bot, binding, err := s.svc.BotRepo.FindByIDWithConvBot(ctx, botID, activity.ConvID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.PermissionDenied, "activity binding is unavailable")
	}
	if err != nil {
		return err
	}
	if binding.ID != bindingID {
		return status.Error(codes.PermissionDenied, "activity binding is stale")
	}
	if bot.Status != "active" || !botpolicy.Subscribed(binding.BotSettings, activity.Type, activity.Seq) {
		return nil
	}
	if err := s.validateActivity(ctx, botID, binding, activity); err != nil {
		return err
	}
	// Builtin activity uses this same seam; only explicit rules persist it as
	// executor input. Unmatched observation is not conversation memory.
	if bot.Type != "third_party" {
		return nil
	}
	bot, current, err := s.svc.BotRepo.FindByIDWithConvBot(ctx, botID, activity.ConvID)
	if err != nil {
		return err
	}
	if current.ID != binding.ID || bot.Status != "active" || !botpolicy.Subscribed(current.BotSettings, activity.Type, activity.Seq) {
		return status.Error(codes.PermissionDenied, "activity subscription changed")
	}
	payload, err := json.Marshal(struct {
		Type         string                  `json:"type"`
		ActivityType string                  `json:"activity_type"`
		BotID        string                  `json:"bot_id"`
		ConvID       string                  `json:"conv_id"`
		BindingID    string                  `json:"binding_id"`
		Activity     *model.ActivityEnvelope `json:"activity"`
	}{"bot.activity", activity.Type, bot.ID, binding.ConvID, binding.ID, activity})
	if err != nil {
		return err
	}
	return s.deliverPayload(ctx, bot, binding.ConvID, payload)
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
		// The HTTP status acknowledges delivery; body cleanup cannot undo it.
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return status.Error(codes.Unavailable, "executor webhook did not acknowledge transport")
		}
		return nil
	default:
		return status.Error(codes.FailedPrecondition, "unsupported executor route")
	}
}
