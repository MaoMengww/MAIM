package model

import "time"

type BillingRecord struct {
	ID           int64     `gorm:"primaryKey;column:id" json:"id"`
	BotID        int64     `gorm:"column:bot_id" json:"bot_id"`
	OwnerID      int64     `gorm:"column:owner_id" json:"owner_id"`
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
