package model

import "time"

type BillingRecord struct {
	ID           string    `gorm:"primaryKey;column:id;type:uuid" json:"id"`
	BotID        *string   `gorm:"column:bot_id;type:uuid" json:"bot_id"`
	OwnerType    string    `gorm:"column:owner_type;not null" json:"owner_type"`
	OwnerID      *string   `gorm:"column:owner_id;type:uuid" json:"owner_id"`
	ModelID      string    `gorm:"column:model_id;type:uuid;not null" json:"model_id"`
	ModelName    string    `gorm:"column:model_name" json:"model_name"`
	Capability   string    `gorm:"column:capability" json:"capability"`
	InputTokens  int       `gorm:"column:input_tokens" json:"input_tokens"`
	OutputTokens int       `gorm:"column:output_tokens" json:"output_tokens"`
	InputCost    float64   `gorm:"column:input_cost" json:"input_cost"`
	OutputCost   float64   `gorm:"column:output_cost" json:"output_cost"`
	Provider     string    `gorm:"column:provider" json:"provider"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (BillingRecord) TableName() string {
	return "billing_records"
}
