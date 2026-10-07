package model

import "time"

type FriendGroup struct {
	ID        string    `gorm:"primaryKey;column:id;type:uuid;not null" json:"id"`
	UserID    string    `gorm:"column:user_id;not null;type:uuid" json:"user_id"`
	Name      string    `gorm:"column:name;size:64;not null" json:"name"`
	SortOrder int32     `gorm:"column:sort_order;not null;default:0" json:"sort_order"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (FriendGroup) TableName() string { return "user.friend_groups" }
