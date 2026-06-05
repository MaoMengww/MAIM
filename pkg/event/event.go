package event

import "encoding/json"

type EventType string

const (
	EventTypeKnowledgeParsing   EventType = "knowledge.parsing"
	EventTypeKnowledgeChunking  EventType = "knowledge.chunking"
	EventTypeKnowledgeEmbedding EventType = "knowledge.embedding"
	EventTypeKnowledgeReady     EventType = "knowledge.ready"
	EventTypeKnowledgeFailed    EventType = "knowledge.failed"
	EventTypeAIThinking         EventType = "ai.thinking"
	EventTypeAIProcessing       EventType = "ai.processing"
	EventTypeWikiIngested       EventType = "wiki.ingested"
	EventTypeWikiIssueFlagged   EventType = "wiki.issue.flagged"
	EventTypeWikiMaintained     EventType = "wiki.maintained"
)

type EventLevel string

const (
	EventLevelInfo    EventLevel = "info"
	EventLevelSuccess EventLevel = "success"
	EventLevelWarning EventLevel = "warning"
	EventLevelError   EventLevel = "error"
)

type RealtimeEvent struct {
	Type      EventType      `json:"type"`
	Level     EventLevel     `json:"level"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Source    string         `json:"source,omitempty"`
	UserID    int64          `json:"user_id,omitempty"`
	ConvID    int64          `json:"conv_id,omitempty"`
	DocID     int64          `json:"doc_id,omitempty"`
	KBID      int64          `json:"kb_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt int64          `json:"created_at"`
}

func MarshalRealtimeEvent(evt RealtimeEvent) ([]byte, error) {
	return json.Marshal(evt)
}

// All consumers (inbox-writer, ws-gateway/signaling, bot-service, audit-service)
// MUST use this struct for deserialization.
type MessageCreatedEvent struct {
	MessageID    int64          `json:"message_id"`
	ConvID       int64          `json:"conv_id"`
	SenderID     int64          `json:"sender_id"`
	SenderType   string         `json:"sender_type"`
	MsgType      int32          `json:"msg_type"`
	Content      map[string]any `json:"content"`
	Seq          int64          `json:"seq"`
	ReplyToMsgID int64          `json:"reply_to_msg_id"`
	CreatedAt    int64          `json:"created_at"`
}

// MessageRecalledEvent is produced on message.recalled topic.
type MessageRecalledEvent struct {
	MessageID int64 `json:"message_id"`
	ConvID    int64 `json:"conv_id"`
	UserID    int64 `json:"user_id"`
}

// MessageEditedEvent is produced on message.edited topic.
type MessageEditedEvent struct {
	MessageID  int64          `json:"message_id"`
	ConvID     int64          `json:"conv_id"`
	UserID     int64          `json:"user_id"`
	NewContent map[string]any `json:"new_content"`
}

// MessageDeletedEvent is produced on message.deleted topic.
type MessageDeletedEvent struct {
	MessageID    int64 `json:"message_id"`
	ConvID       int64 `json:"conv_id"`
	UserID       int64 `json:"user_id"`
	DeleteForAll bool  `json:"delete_for_all"`
}

// ConversationReadUpdatedEvent is produced on conversation.read.updated topic.
type ConversationReadUpdatedEvent struct {
	ConvID      int64 `json:"conv_id"`
	UserID      int64 `json:"user_id"`
	LastReadSeq int64 `json:"last_read_seq"`
}

// BroadcastCreatedEvent is produced on message.created topic for broadcast messages.
type BroadcastCreatedEvent struct {
	BroadcastID   int64  `json:"broadcast_id"`
	SenderID      int64  `json:"sender_id"`
	Content       string `json:"content"`
	Scope         string `json:"scope"`
	ScopeTargetID int64  `json:"scope_target_id"`
	CreatedAt     int64  `json:"created_at"`
}

// BotAddedToConvEvent is produced on conversation.bot.added topic.
type BotAddedToConvEvent struct {
	ConvID  int64  `json:"conv_id"`
	BotID   int64  `json:"bot_id"`
	AddedBy int64  `json:"added_by"`
	BotName string `json:"bot_name"`
	BotType string `json:"bot_type"`
}

// BotRemovedFromConvEvent is produced on conversation.bot.removed topic.
type BotRemovedFromConvEvent struct {
	ConvID    int64 `json:"conv_id"`
	BotID     int64 `json:"bot_id"`
	RemovedBy int64 `json:"removed_by"`
}

// MemberJoinedEvent is produced on conversation.member.joined topic.
type MemberJoinedEvent struct {
	ConvID   int64   `json:"conv_id"`
	UserIDs  []int64 `json:"user_ids"`
	JoinedBy int64   `json:"joined_by"`
}

// MemberLeftEvent is produced on conversation.member.left topic.
type MemberLeftEvent struct {
	ConvID    int64   `json:"conv_id"`
	UserIDs   []int64 `json:"user_ids"`
	RemovedBy int64   `json:"removed_by"`
}

// WebhookPayload is the JSON body sent to third-party webhook callback URLs.
type WebhookPayload struct {
	EventType    string         `json:"event_type"`
	EventID      string         `json:"event_id"`
	BotID        string         `json:"bot_id"`
	ConvID       string         `json:"conv_id"`
	Message      map[string]any `json:"message,omitempty"`
	Sender       map[string]any `json:"sender,omitempty"`
	Conversation map[string]any `json:"conversation,omitempty"`
}
