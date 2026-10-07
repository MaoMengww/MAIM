package model

import "time"

type Friend struct {
	ID        string    `gorm:"primaryKey;column:id;type:uuid;not null" json:"id"`
	UserID    string    `gorm:"column:user_id;not null;type:uuid" json:"user_id"`
	FriendID  string    `gorm:"column:friend_id;not null;type:uuid" json:"friend_id"`
	GroupID   *string   `gorm:"column:group_id;type:uuid" json:"group_id"`
	Remark    string    `gorm:"column:remark;size:64;not null;default:''" json:"remark"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (Friend) TableName() string { return "user.friends" }
