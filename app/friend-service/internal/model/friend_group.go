package model

import "time"

type FriendGroup struct {
	ID        int64     `gorm:"primaryKey;column:id" json:"id"`
	UserID    int64     `gorm:"column:user_id;not null" json:"user_id"`
	Name      string    `gorm:"column:name;size:64;not null" json:"name"`
	SortOrder int32     `gorm:"column:sort_order;not null;default:0" json:"sort_order"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (FriendGroup) TableName() string { return "friend_groups" }
