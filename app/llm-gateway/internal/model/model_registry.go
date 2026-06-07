package model

import "time"

type ModelRegistry struct {
	ID                 int64          `gorm:"primaryKey;column:id" json:"id"`
	ModelName          string         `gorm:"column:model_name" json:"model_name"`
	Provider           string         `gorm:"column:provider" json:"provider"`
	Capability         string         `gorm:"column:capability" json:"capability"`
	BaseURL            string         `gorm:"column:base_url" json:"base_url"`
	APIKeyEncrypted    string         `gorm:"column:api_key_encrypted" json:"-"`
	ContextWindow      int            `gorm:"column:context_window" json:"context_window"`
	MaxOutputTokens    int            `gorm:"column:max_output_tokens" json:"max_output_tokens"`
	InputPricePerMTok  float64        `gorm:"column:input_price_per_mtok;type:double precision" json:"input_price_per_mtok"`
	OutputPricePerMTok float64        `gorm:"column:output_price_per_mtok;type:double precision" json:"output_price_per_mtok"`
	Status             string         `gorm:"column:status" json:"status"`
	OwnerID            int64          `gorm:"column:owner_id" json:"owner_id"`
	Metadata           map[string]any `gorm:"column:metadata;serializer:json" json:"metadata"`
	CreatedAt          time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (ModelRegistry) TableName() string {
	return "model_registry"
}
