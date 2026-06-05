package model

import "time"

const (
	MemberTypeUser = "user"
	MemberTypeBot  = "bot"
)

type Bot struct {
	ID     int64  `gorm:"primaryKey;column:id"`
	Name   string `gorm:"column:name"`
	Avatar string `gorm:"column:avatar"`
}

func (Bot) TableName() string {
	return "bot.bots"
}

type Conversation struct {
	ID                 int64     `gorm:"primaryKey;column:id"`
	Type               int32     `gorm:"column:type"`
	Name               string    `gorm:"column:name"`
	Avatar             string    `gorm:"column:avatar"`
	OwnerID            int64     `gorm:"column:owner_id"`
	Announcement       string    `gorm:"column:announcement"`
	IsMutedAll         bool      `gorm:"column:is_muted_all"`
	Background         string    `gorm:"column:background"`
	MaxSeq             int64     `gorm:"column:max_seq"`
	LastMessageID      int64     `gorm:"column:last_message_id"`
	LastMessagePreview string    `gorm:"column:last_message_preview"`
	MemberCount        int32     `gorm:"column:member_count"`
	CreatedAt          time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt          time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

type ConversationMember struct {
	ID         int64     `gorm:"primaryKey;column:id"`
	ConvID     int64     `gorm:"column:conv_id"`
	UserID     int64     `gorm:"column:user_id"`
	MemberType string    `gorm:"column:member_type"`
	BotID      int64     `gorm:"column:bot_id"`
	Role       int32     `gorm:"column:role"`
	Alias      string    `gorm:"column:alias"`
	IsMuted    bool      `gorm:"column:is_muted"`
	MuteUntil  int64     `gorm:"column:mute_until"`
	JoinedAt   time.Time `gorm:"column:joined_at;autoCreateTime"`
}

func (ConversationMember) TableName() string {
	return "conv_members"
}

type ConvReadSeq struct {
	ID          int64     `gorm:"primaryKey;column:id"`
	ConvID      int64     `gorm:"column:conv_id"`
	UserID      int64     `gorm:"column:user_id"`
	LastReadSeq int64     `gorm:"column:last_read_seq"`
	ReadAt      time.Time `gorm:"column:read_at;autoCreateTime"`
}

type ConvSettings struct {
	ID       int64 `gorm:"primaryKey;column:id"`
	ConvID   int64 `gorm:"column:conv_id"`
	UserID   int64 `gorm:"column:user_id"`
	IsMuted  bool  `gorm:"column:is_muted"`
	IsPinned bool  `gorm:"column:is_pinned"`
}
