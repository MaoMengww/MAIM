package botplatform

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-platform/internal/model"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/consts"
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

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
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
