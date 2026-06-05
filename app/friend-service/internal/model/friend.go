package model

import "time"

type Friend struct {
	ID        int64     `gorm:"primaryKey;column:id" json:"id"`
	UserID    int64     `gorm:"column:user_id;not null" json:"user_id"`
	FriendID  int64     `gorm:"column:friend_id;not null" json:"friend_id"`
	GroupID   int64     `gorm:"column:group_id;not null;default:0" json:"group_id"`
	Remark    string    `gorm:"column:remark;size:64;not null;default:''" json:"remark"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (Friend) TableName() string { return "friends" }
