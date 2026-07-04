package graph

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"
)

// KnowledgeSource represents a knowledge base source attached to a bot reply.
type KnowledgeSource struct {
	Type    string `json:"type"` // "rag" or "wiki"
	KbName  string `json:"kb_name"`
	KbID    int64  `json:"kb_id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// StreamChunk is a streaming output chunk.
type StreamChunk struct {
	Type     string
	Content  string
	ToolName string
}

// MemoryStore is the memory retrieval interface used by BuildContext.
type MemoryStore interface {
	Retrieve(ctx context.Context, botID, userID int64, ownerID int64, embeddingModelID int64, query string, limit int) ([]MemoryItem, error)
	GetProfile(ctx context.Context, botID, userID int64) string
}

// MemoryItem is a retrieved memory fact.
type MemoryItem struct {
	ID         int64
	Content    string
	Type       string
	Importance float64
}

// MsgClient abstracts gRPC calls to message-service.
type MsgClient interface {
	GetRecentMessages(ctx context.Context, convID, userID int64, limit int) ([]Message, error)
	SendBotReply(ctx context.Context, botID, convID int64, text string, replyTo int64, rawPayload ...string) (int64, error)
}

// KbClient abstracts gRPC calls to knowledge-base.
type KbClient interface {
	Retrieve(ctx context.Context, query string, botID, convID int64, topK int, kbIDs []int64) ([]KbDocument, error)
	WikiQuery(ctx context.Context, query string, wikiKBIDs []int64, modelID int64, modelName string, history string) (*WikiResult, error)
	ListBoundKBs(ctx context.Context, botID, convID int64) ([]BoundKB, error)
}

// UserNamesFunc resolves user IDs to display names.
type UserNamesFunc func(ctx context.Context, userIDs []int64) (map[int64]string, error)

// Message is a simplified message from message-service.
type Message struct {
	MsgID      int64
	SenderID   int64
	SenderName string
	Content    string
	MsgType    int32
	Seq        int64
	CreatedAt  int64
}

// KbDocument is a knowledge base document snippet.
type KbDocument struct {
	DocID          string
	Title          string
	Content        string
	MatchedContent string
	Score          float64
	KbID           int64
	KbName         string
}

// WikiResult is the result of a wiki query.
type WikiResult struct {
	Answer     string
	References []WikiReference
}

// WikiReference is a single wiki page reference.
type WikiReference struct {
	Slug    string
	Title   string
	Snippet string
}

// BoundKB represents a knowledge base binding with mode info.
type BoundKB struct {
	KBID int64
	Mode string // "rag" or "wiki"
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
	return fmt.Sprintf("user_%d", m.SenderID)
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
