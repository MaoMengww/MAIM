package model

import "time"

type ModelRegistry struct {
	ID                 string         `gorm:"primaryKey;column:id;type:uuid" json:"id"`
	ModelName          string         `gorm:"column:model_name" json:"model_name"`
	ConnectionID       string         `gorm:"column:connection_id;type:uuid" json:"connection_id"`
	RequestsPerMinute  int            `gorm:"column:requests_per_minute" json:"requests_per_minute"`
	RequestsPerSecond  int            `gorm:"column:requests_per_second" json:"requests_per_second"`
	TokensPerSecond    int            `gorm:"column:tokens_per_second" json:"tokens_per_second"`
	Concurrency        int            `gorm:"column:concurrency" json:"concurrency"`
	Capability         string         `gorm:"column:capability" json:"capability"`
	ContextWindow      int            `gorm:"column:context_window" json:"context_window"`
	MaxOutputTokens    int            `gorm:"column:max_output_tokens" json:"max_output_tokens"`
	InputPricePerMTok  float64        `gorm:"column:input_price_per_mtok;type:double precision" json:"input_price_per_mtok"`
	OutputPricePerMTok float64        `gorm:"column:output_price_per_mtok;type:double precision" json:"output_price_per_mtok"`
	Status             string         `gorm:"column:status" json:"status"`
	OwnerType          string         `gorm:"column:owner_type;not null" json:"owner_type"`
	OwnerID            *string        `gorm:"column:owner_id;type:uuid" json:"owner_id"`
	Metadata           map[string]any `gorm:"column:metadata;serializer:json" json:"metadata"`
	CreatedAt          time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (ModelRegistry) TableName() string {
	return "model_registry"
}
