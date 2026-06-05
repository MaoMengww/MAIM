package model

import "time"

type UserBlock struct {
	ID            int64     `gorm:"primaryKey;column:id" json:"id"`
	UserID        int64     `gorm:"column:user_id;not null" json:"user_id"`
	BlockedUserID int64     `gorm:"column:blocked_user_id;not null" json:"blocked_user_id"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (UserBlock) TableName() string { return "user_blocks" }
