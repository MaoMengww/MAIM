package memory

import (
	"context"
	"time"
)

// MemoryItem is the core memory entity.
type MemoryItem struct {
	ID             int64
	BotID          int64
	UserID         int64
	MemoryType     string // "fact" | "episode"
	Content        string
	Subject        string
	Predicate      string
	Object         string
	Category       string
	Importance     float64
	Confidence     float64
	AccessCount    int
	FinalScore     float64 `gorm:"-" json:"final_score"`
	LastAccessedAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Store is the memory storage interface.
type Store interface {
	Insert(ctx context.Context, item *MemoryItem) error
	Update(ctx context.Context, item *MemoryItem) error
	Delete(ctx context.Context, id int64) error
	DeleteByUser(ctx context.Context, botID, userID int64) error
	FindByID(ctx context.Context, id int64) (*MemoryItem, error)
	FindByUser(ctx context.Context, botID, userID int64, limit int) ([]*MemoryItem, error)
	CountByUser(ctx context.Context, botID, userID int64) (int64, error)
	SearchByUser(ctx context.Context, botID, userID int64, query string, limit int) ([]*MemoryItem, error)
	TouchBatch(ctx context.Context, ids []int64) error
}
