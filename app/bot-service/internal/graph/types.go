package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"
)

// KnowledgeSource represents a knowledge base source attached to a bot reply.
type KnowledgeSource struct {
	Type    string `json:"type"` // "rag"
	KbName  string `json:"kb_name"`
	KbID    string `json:"kb_id"`
	DocID   string `json:"doc_id"`
	ChunkID string `json:"chunk_id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// BuildRawPayload preserves reply sources and tool usage in the persisted body.
func BuildRawPayload(kbSources []KnowledgeSource, usedTools []string) string {
	if len(kbSources) == 0 && len(usedTools) == 0 {
		return ""
	}
	payload := map[string]any{"kb_sources": kbSources}
	if len(usedTools) > 0 {
		payload["tool_names"] = usedTools
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(b)
}

// StreamChunk is a streaming output chunk.
type StreamChunk struct {
	Type     string
	Content  string
	ToolName string
}

// MemoryStore is the memory retrieval interface used by BuildContext.
type MemoryStore interface {
	Retrieve(ctx context.Context, botID, userID string, ownerID *string, embeddingModelID *string, query string, limit int) ([]MemoryItem, error)
	GetProfile(ctx context.Context, botID, userID string) string
}

// MemoryItem is a retrieved memory fact.
type MemoryItem struct {
	ID         string
	Content    string
	Type       string
	Importance float64
}

// MsgClient abstracts gRPC calls to message-service.
type MsgClient interface {
	GetRecentMessages(ctx context.Context, convID, userID string, limit int) ([]Message, error)
	SendBotReply(ctx context.Context, botID, convID string, text string, replyTo *string, rawPayload ...string) (string, error)
}

// KbClient abstracts gRPC calls to knowledge-base.
type KbClient interface {
	Retrieve(ctx context.Context, query string, botID, convID string, topK int, kbIDs []string) ([]KbDocument, error)
	ListBoundKBs(ctx context.Context, botID, convID string) ([]BoundKB, error)
}

// UserNamesFunc resolves user IDs to display names.
type UserNamesFunc func(ctx context.Context, userIDs []string) (map[string]string, error)

// Message is a simplified message from message-service.
type Message struct {
	MsgID      string
	SenderID   *string
	BotID      string
	SenderName string
	Content    string
	MsgType    int32
	Seq        int64
	CreatedAt  int64
}

// KbDocument is a knowledge base document snippet.
type KbDocument struct {
	ChunkID        string
	DocID          string
	Title          string
	Content        string
	MatchedContent string
	Score          float64
	KbID           string
	KbName         string
}

// BoundKB represents a knowledge base binding with mode info.
type BoundKB struct {
	KBID string
	Mode string // "rag"
	Name string
}

// FormatMemories formats memory items for prompt injection.
func FormatMemories(items []MemoryItem) string {
	if len(items) == 0 {
		return ""
	}
	out := "What I know about you:\n"
	for i, m := range items {
		out += fmt.Sprintf("%d. %s\n", i+1, m.Content)
	}
	return out
}

// FormatKnowledge formats knowledge base documents for prompt injection.
func FormatKnowledge(docs []KbDocument) string {
	if len(docs) == 0 {
		return ""
	}
	out := ""
	for i, d := range docs {
		out += fmt.Sprintf("【参考资料 %d】来源: %s\n%s\n\n", i+1, d.Title, d.Content)
	}
	return out
}

// FormatHistory formats recent messages for prompt injection.
func FormatHistory(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}
	out := "最近的对话:\n"
	for _, m := range msgs {
		out += fmt.Sprintf("[%s][%s]: %s\n", formatMessageTime(m.CreatedAt), messageSenderName(m), m.Content)
	}
	return out
}

func messageSenderName(m Message) string {
	if m.SenderName != "" {
		return m.SenderName
	}
	if m.SenderID != nil {
		return "user_" + *m.SenderID
	}
	if m.BotID != "" {
		return "bot_" + m.BotID
	}
	return "system"
}

func formatMessageTime(ts int64) string {
	if ts <= 0 {
		return "unknown_time"
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	runes := utf8.RuneCountInString(s)
	tokens := (runes + 1) / 2
	if tokens == 0 {
		return 1
	}
	return tokens
}

// WeekdayName returns the weekday name.
func WeekdayName(d time.Time) string {
	names := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	return names[d.Weekday()]
}
