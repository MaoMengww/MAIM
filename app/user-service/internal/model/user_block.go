package model

import "time"

type UserBlock struct {
	ID            string    `gorm:"primaryKey;column:id;type:uuid;not null" json:"id"`
	UserID        string    `gorm:"column:user_id;not null;type:uuid" json:"user_id"`
	BlockedUserID string    `gorm:"column:blocked_user_id;not null;type:uuid" json:"blocked_user_id"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (UserBlock) TableName() string { return "user.user_blocks" }
