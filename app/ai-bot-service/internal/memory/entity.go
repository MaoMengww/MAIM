package memory

import (
	"context"
	"time"
)

// Scope identifies a bot-user memory space with optional vector lookup info.
type Scope struct {
	BotID            int64
	UserID           int64
	ConvID           int64
	OwnerID          int64
	EmbeddingModelID int64
}

// MemoryQuery carries the full context needed for retrieval.
type MemoryQuery struct {
	BotID            int64
	UserID           int64
	OwnerID          int64
	EmbeddingModelID int64
	Query            string
	Limit            int
}

// Entity is a Neo4j Entity node representing a named thing (person, org, tool, etc.).
type Entity struct {
	BotID          int64
	UserID         int64
	Name           string
	NormalizedName string
	EntityType     string
	Aliases        []string
	FirstSeen      time.Time
	LastSeen       time.Time
}

// Episode stores the original user message that memory facts are extracted from.
type Episode struct {
	ID        int64
	BotID     int64
	UserID    int64
	ConvID    int64
	MsgID     int64
	Actor     string
	Content   string
	CreatedAt time.Time
}

// Fact is a temporal memory fact extracted from an episode.
type Fact struct {
	ID           int64
	BotID        int64
	UserID       int64
	ConvID       int64
	MsgID        int64
	Subject      string
	Predicate    string
	Object       string
	EntityType   string
	Category     string
	Content      string
	Evidence     string
	Importance   float64
	Confidence   float64
	ValidAt      time.Time
	InvalidAt    *time.Time
	CreatedAt    time.Time
	ExpiredAt    *time.Time
	TemporalHint string
	SearchText   string
}

// Memory is a fact returned for prompt injection.
type Memory struct {
	ID           int64
	Content      string
	Category     string
	Importance   float64
	Confidence   float64
	ValidAt      time.Time
	InvalidAt    *time.Time
	CreatedAt    time.Time
	ExpiredAt    *time.Time
	TemporalHint string

	// Retrieval metadata used for ranking/debug logs; not persisted.
	Source      string
	SourceScore float64
	Hops        int
	Rank        int
	RankScore   float64
	FinalScore  float64
}

// Store persists and searches temporal memory.
type Store interface {
	SaveEpisode(ctx context.Context, episode *Episode) error
	AddFacts(ctx context.Context, facts []Fact) error
	Search(ctx context.Context, scope Scope, query string, limit int) ([]Memory, error)
	SearchByIDs(ctx context.Context, scope Scope, ids []int64, historical bool) ([]Memory, error)
	SearchWithTraversal(ctx context.Context, scope Scope, query string, limit int, maxHops int) ([]Memory, error)
	GetProfileData(ctx context.Context, botID, userID int64) (profileText string, updatedAt time.Time, err error)
	CountNewFactsSince(ctx context.Context, botID, userID int64, since time.Time) (int, error)
	GetIncrementalFacts(ctx context.Context, botID, userID int64, since time.Time) (newFacts []ProfileFact, expiredFacts []ProfileFact, err error)
	GetInitialProfileFacts(ctx context.Context, botID, userID int64, limit int) ([]ProfileFact, error)
	UpdateProfile(ctx context.Context, botID, userID int64, profileText string, updatedAt time.Time) error
}

// ProfileFact is a lightweight fact used for profile generation.
type ProfileFact struct {
	Content      string
	Category     string
	Importance   float64
	TemporalHint string
}
