package model

import "time"

type ConvBot struct {
	ID               int64          `gorm:"primaryKey;column:id"`
	ConvID           int64          `gorm:"column:conv_id"`
	BotID            int64          `gorm:"column:bot_id"`
	AddedBy          int64          `gorm:"column:added_by"`
	ResponseTriggers []string       `gorm:"column:response_triggers;type:jsonb;serializer:json"`
	BotSettings      map[string]any `gorm:"column:bot_settings;serializer:json"`
	CreatedAt        time.Time      `gorm:"column:created_at;autoCreateTime"`
}

func (ConvBot) TableName() string { return "conv_bots" }
