package model

import "time"

type ConvBot struct {
	ID          string         `gorm:"primaryKey;type:uuid;column:id"`
	ConvID      string         `gorm:"type:uuid;column:conv_id"`
	BotID       string         `gorm:"type:uuid;column:bot_id"`
	AddedBy     string         `gorm:"type:uuid;column:added_by"`
	BotSettings map[string]any `gorm:"column:bot_settings;serializer:json"`
	CreatedAt   time.Time      `gorm:"column:created_at;autoCreateTime"`
}

func (ConvBot) TableName() string { return "conv_bots" }
