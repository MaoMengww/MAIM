package model

import (
	"time"
)

// Memory is the GORM model for the bot_memories table.
type Memory struct {
	ID             int64      `gorm:"primaryKey;column:id" json:"id"`
	BotID          int64      `gorm:"column:bot_id" json:"bot_id"`
	UserID         int64      `gorm:"column:user_id" json:"user_id"`
	MemoryType     string     `gorm:"column:memory_type" json:"memory_type"`
	Content        string     `gorm:"column:content" json:"content"`
	Importance     float64    `gorm:"column:importance" json:"importance"`
	AccessCount    int        `gorm:"column:access_count" json:"access_count"`
	LastAccessedAt *time.Time `gorm:"column:last_accessed_at" json:"last_accessed_at"`
	MilvusID       string     `gorm:"column:milvus_id" json:"milvus_id"`
	CreatedAt      time.Time  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (Memory) TableName() string { return "bot_memories" }

// BotEvent represents an incoming Kafka event for a bot.
type BotEvent struct {
	EventType        string          `json:"event_type"`
	BotID            int64           `json:"bot_id"`
	ConvID           int64           `json:"conv_id"`
	Message          *EventMessage   `json:"message"`
	Sender           *EventSender    `json:"sender"`
	MentionedUserIDs []int64         `json:"mentioned_user_ids"`
	BotConfig        *EventBotConfig `json:"bot_config"`
}

// EventMessage is the message part of a bot event.
type EventMessage struct {
	MsgID        int64  `json:"msg_id"`
	Text         string `json:"text"`
	MsgType      int32  `json:"msg_type"`
	ReplyToMsgID int64  `json:"reply_to_msg_id"`
}

// EventSender is the sender part of a bot event.
type EventSender struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Language string `json:"language"`
}

// EventBotConfig is the bot config snippet carried in a bot event.
type EventBotConfig struct {
	ResponseTriggers []string `json:"response_triggers"`
	UsePlatformModel bool     `json:"use_platform_model"`
}

// StreamChunk is used for streaming output.
type StreamChunk struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name"`
	MessageID string `json:"message_id"`
	ConvID    int64  `json:"conv_id"`
}

// ExtractedMemories is the LLM output for memory extraction.
type ExtractedMemories struct {
	Facts   []ExtractedFact `json:"facts"`
	Episode *string         `json:"episode"`
}

// ExtractedFact is a single fact extracted by the memory LLM.
type ExtractedFact struct {
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
}
