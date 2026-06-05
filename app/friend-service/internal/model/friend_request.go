package model

import "time"

const (
	FriendRequestStatusPending   int32 = 1
	FriendRequestStatusAccepted  int32 = 2
	FriendRequestStatusRejected  int32 = 3
	FriendRequestStatusCancelled int32 = 4
)

type FriendRequest struct {
	ID         int64     `gorm:"primaryKey;column:id" json:"id"`
	FromUserID int64     `gorm:"column:from_user_id;not null" json:"from_user_id"`
	ToUserID   int64     `gorm:"column:to_user_id;not null" json:"to_user_id"`
	Message    string    `gorm:"column:message;size:256;not null;default:''" json:"message"`
	Status     int32     `gorm:"column:status;not null;default:0" json:"status"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (FriendRequest) TableName() string { return "friend_requests" }
