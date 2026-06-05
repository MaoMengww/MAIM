package model

import "time"

// ConvBot mirrors the conv_bots table.
type ConvBot struct {
	ID               int64          `gorm:"primaryKey;column:id" json:"id"`
	ConvID           int64          `gorm:"column:conv_id" json:"conv_id"`
	BotID            int64          `gorm:"column:bot_id" json:"bot_id"`
	AddedBy          int64          `gorm:"column:added_by" json:"added_by"`
	ResponseTriggers []string       `gorm:"column:response_triggers;type:jsonb;serializer:json" json:"response_triggers"`
	BotSettings      map[string]any `gorm:"column:bot_settings;serializer:json" json:"bot_settings"`
	CreatedAt        time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (ConvBot) TableName() string { return "conv.conv_bots" }
