package model

import (
	"time"
)

type Bot struct {
	ID                       int64     `gorm:"primaryKey;column:id"`
	OwnerID                  int64     `gorm:"column:owner_id;default:0"`
	Name                     string    `gorm:"column:name"`
	Avatar                   string    `gorm:"column:avatar"`
	Type                     string    `gorm:"column:type"`
	TemplateID               string    `gorm:"column:template_id"` // "qa" | "knowledge" (official only)
	SubType                  string    `gorm:"column:sub_type"`    // "webhook" | "ws" (third_party only)
	Status                   string    `gorm:"column:status;default:active"`
	UsePlatformModel         bool      `gorm:"column:use_platform_model;default:true"`
	ModelName                string    `gorm:"column:model_name"`
	ModelID                  int64     `gorm:"column:model_id;default:0"`
	BaseURL                  string    `gorm:"column:base_url"`
	APIKeyEncrypted          string    `gorm:"column:api_key_encrypted"`
	SystemPrompt             string    `gorm:"column:system_prompt"`
	Persona                  string    `gorm:"column:persona"`
	EnableKnowledge          bool      `gorm:"column:enable_knowledge;default:true"`
	Temperature              float64   `gorm:"column:temperature;default:0.7"`
	MaxContextMessages       int       `gorm:"column:max_context_messages;default:10"`
	MaxContextTokens         int       `gorm:"column:max_context_tokens;default:0"`
	StreamingEnabled         bool      `gorm:"column:streaming_enabled;default:true"`
	MemoryModelName          string    `gorm:"column:memory_model_name"`
	MemoryModelID            int64     `gorm:"column:memory_model_id;default:0"`
	MemoryUsePlatformModel   bool      `gorm:"column:memory_use_platform_model;default:true"`
	MemoryLimit              int       `gorm:"column:memory_limit;default:0"`
	MemoryEmbeddingModelName string    `gorm:"column:memory_embedding_model_name"`
	MemoryEmbeddingModelID   int64     `gorm:"column:memory_embedding_model_id;default:0"`
	MemoryAPIKeyEncrypted    string    `gorm:"column:memory_api_key_encrypted"`
	ConnMode                 string    `gorm:"column:conn_mode"`
	WebhookSecret            string    `gorm:"column:webhook_secret"`
	CallbackURL              string    `gorm:"column:callback_url"`
	AppSecretHash            string    `gorm:"column:app_secret_hash"`
	BotTags                  []string  `gorm:"column:bot_tags;type:jsonb;serializer:json"`
	ResponseTriggers         []string  `gorm:"column:response_triggers;type:jsonb;serializer:json"`
	Capabilities             []byte    `gorm:"column:capabilities;type:jsonb"`
	Settings                 []byte    `gorm:"column:settings;type:jsonb"`
	CreatedAt                time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt                time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (Bot) TableName() string {
	return "bots"
}
