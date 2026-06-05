package model

import "time"

type FailedEvent struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	Topic       string    `gorm:"size:64;not null"`
	Key         string    `gorm:"size:64;not null"`
	Payload     []byte    `gorm:"type:jsonb;not null"`
	RetryCount  int       `gorm:"default:0"`
	LastError   string    `gorm:"size:256"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}

func (FailedEvent) TableName() string {
	return "msg.failed_events"
}
