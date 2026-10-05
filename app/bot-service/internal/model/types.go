package model

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
