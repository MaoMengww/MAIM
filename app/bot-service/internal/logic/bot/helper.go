package bot

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
)

// Bot type constants
// Bot type constants are defined in pkg/consts — use consts.BotType* directly

// Sub type constants (third_party only)
const (
	SubTypeWS      = "ws"
	SubTypeWebhook = "webhook"
)

// Template constants (official only)
const (
	TemplateQA        = "qa"
	TemplateKnowledge = "knowledge"
)

// Conn mode constants
const (
	ConnModeWS      = "ws"
	ConnModeWebhook = "webhook"
)

// NormalizeBotType converts a proto bot type string to the internal type.
func NormalizeBotType(t string) string {
	switch strings.ToLower(t) {
	case "bot_type_official", "official":
		return consts.BotTypeOfficial
	case "bot_type_self_deployed", "self_deployed":
		return consts.BotTypeSelfDeployed
	case "bot_type_third_party", "third_party":
		return consts.BotTypeThirdParty
	default:
		return t
	}
}

// NormalizeConnMode converts a proto conn mode string to the internal value.
func NormalizeConnMode(mode string) string {
	switch strings.ToLower(mode) {
	case "conn_mode_ws", "ws":
		return ConnModeWS
	case "conn_mode_webhook", "webhook":
		return ConnModeWebhook
	default:
		return ConnModeWS
	}
}

func modelBotToProto(b *model.Bot) *pb.Bot {
	if b == nil {
		return nil
	}
	pbBot := &pb.Bot{
		Id:                       b.ID,
		OwnerType:                b.OwnerType,
		OwnerId:                  b.OwnerID,
		Name:                     b.Name,
		Avatar:                   b.Avatar,
		Type:                     b.Type,
		Status:                   b.Status,
		UsePlatformModel:         b.UsePlatformModel,
		ModelName:                b.ModelName,
		ModelId:                  b.ModelID,
		BaseUrl:                  b.BaseURL,
		SystemPrompt:             b.SystemPrompt,
		Persona:                  b.Persona,
		EnableKnowledge:          b.EnableKnowledge,
		Temperature:              b.Temperature,
		MaxContextMessages:       int32(b.MaxContextMessages),
		MaxContextTokens:         int32(b.MaxContextTokens),
		StreamingEnabled:         b.StreamingEnabled,
		MemoryModelName:          b.MemoryModelName,
		MemoryModelId:            b.MemoryModelID,
		MemoryUsePlatformModel:   b.MemoryUsePlatformModel,
		MemoryLimit:              int32(b.MemoryLimit),
		MemoryEmbeddingModelName: b.MemoryEmbeddingModelName,
		MemoryEmbeddingModelId:   b.MemoryEmbeddingModelID,
		ConnMode:                 b.ConnMode,
		CallbackUrl:              b.CallbackURL,
		TemplateId:               b.TemplateID,
		SubType:                  b.SubType,
		HasWebhookSecret:         b.WebhookSecret != "",
		HasAppSecret:             b.AppSecretHash != "",
		BotTags:                  b.BotTags,
		ResponseTriggers:         b.ResponseTriggers,
		Capabilities:             string(b.Capabilities),
		Settings:                 string(b.Settings),
		CreatedAt:                b.CreatedAt.Unix(),
		UpdatedAt:                b.UpdatedAt.Unix(),
	}
	return pbBot
}

func verifyWebhookSignature(message []byte, signature string, timestamp int64, secret string) error {
	now := time.Now().Unix()
	if abs(now-timestamp) > 300 {
		return fmt.Errorf("webhook timestamp expired")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("webhook signature mismatch")
	}
	return nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func generateRandomSecret(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func validateIDs(ids ...string) error {
	for _, id := range ids {
		if identity.Validate(id) != nil {
			return ErrBotInvalid
		}
	}
	return nil
}

func validateCaller(ctx context.Context, requestedUser string, ids ...string) error {
	caller, _ := ctx.Value(interceptor.ContextKeyUserID).(string)
	if identity.Validate(caller) != nil || requestedUser != caller {
		return ErrBotForbidden
	}
	return validateIDs(ids...)
}

func validateOwnership(ownerType string, ownerID *string) error {
	switch ownerType {
	case "platform":
		if ownerID == nil {
			return nil
		}
	case "user":
		if ownerID != nil {
			return validateIDs(*ownerID)
		}
	}
	return ErrBotInvalid
}

func validateUserOwnership(ctx context.Context, ownerType string, ownerID *string) error {
	if err := validateOwnership(ownerType, ownerID); err != nil {
		return err
	}
	// Platform entities are seeded internally. A public request cannot elevate ownership.
	if ownerType != "user" || ownerID == nil {
		return ErrBotForbidden
	}
	return validateCaller(ctx, *ownerID)
}

func selectModel(ctx context.Context, r repo.BotRepoInterface, modelID *string, name, userID string, platform bool) (*string, bool, error) {
	if modelID != nil {
		if err := validateIDs(*modelID); err != nil {
			return nil, false, err
		}
	} else if name != "" {
		id, err := r.ResolveModelID(ctx, name, userID, platform)
		if err != nil {
			return nil, false, err
		}
		modelID = &id
	}
	if modelID == nil {
		return nil, false, nil
	}
	ownerType, ownerID, err := r.GetModelOwner(ctx, *modelID)
	if err != nil {
		return nil, false, err
	}
	if err := validateOwnership(ownerType, ownerID); err != nil {
		return nil, false, err
	}
	if ownerType == "user" && *ownerID != userID {
		return nil, false, ErrBotForbidden
	}
	return modelID, ownerType == "platform", nil
}

func canWrite(bot *model.Bot, callerID string) bool {
	return bot != nil && bot.OwnerType == "user" && bot.OwnerID != nil &&
		identity.Validate(callerID) == nil && *bot.OwnerID == callerID
}

func validateReferenceUpdate(id *string, clear bool, name string) error {
	if clear && (id != nil || name != "") {
		return ErrBotInvalid
	}
	if id != nil {
		return validateIDs(*id)
	}
	return nil
}
