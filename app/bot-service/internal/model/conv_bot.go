package model

import "time"

// ConvBot mirrors the message domain's conv_bots table.
type ConvBot struct {
	ID          string         `gorm:"primaryKey;column:id;type:uuid" json:"id"`
	ConvID      string         `gorm:"column:conv_id;type:uuid" json:"conv_id"`
	BotID       string         `gorm:"column:bot_id;type:uuid" json:"bot_id"`
	AddedBy     string         `gorm:"column:added_by;type:uuid" json:"added_by"`
	BotSettings map[string]any `gorm:"column:bot_settings;serializer:json" json:"bot_settings"`
	CreatedAt   time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (ConvBot) TableName() string { return "messaging.conv_bots" }
