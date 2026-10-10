package model

import (
	"encoding/json"
	"time"
)

// WorkingSummary is derived, untrusted work material, never authority or memory.
type WorkingSummary struct {
	Goal                 string   `json:"goal"`
	ConfirmedConstraints []string `json:"confirmed_constraints"`
	Completed            []string `json:"completed"`
	Pending              []string `json:"pending"`
}

// RunCompactionCheckpoint changes the model view, not the append-only work log.
// Coverage uses trajectory positions; InputUpperBound is an independent watermark.
type RunCompactionCheckpoint struct {
	ID                 string `gorm:"type:uuid;primaryKey"`
	RunID              string `gorm:"type:uuid"`
	InteractionID      string `gorm:"type:uuid"`
	Version            int64
	BranchVersion      int64
	SourceRevision     int64
	CoveredFrom        int64
	CoveredThrough     int64
	InputUpperBound    int64
	PriorCheckpointID  *string         `gorm:"type:uuid"`
	SourceVersions     json.RawMessage `gorm:"type:jsonb"`
	InstructionVersion string
	ToolsVersion       string
	AssemblyVersion    string
	Summary            json.RawMessage `gorm:"type:jsonb"`
	PinnedMessages     json.RawMessage `gorm:"type:jsonb"`
	Omissions          json.RawMessage `gorm:"type:jsonb"`
	Status             string
	CreatedAt          time.Time
}

func (RunCompactionCheckpoint) TableName() string { return "bot.run_compaction_checkpoints" }

// Summary calls use the same gateway but have their own durable cumulative cap;
// they do not spend or reset the work model/tool iteration budget.
type RunCompactionCall struct {
	ID                  string `gorm:"type:uuid;primaryKey"`
	RunID               string `gorm:"type:uuid"`
	InteractionID       string `gorm:"type:uuid"`
	ExecutionGeneration int64
	BranchVersion       int64
	SourceRevision      int64
	CoveredFrom         int64
	CoveredThrough      int64
	InputUpperBound     int64
	PriorCheckpointID   *string         `gorm:"type:uuid"`
	Request             json.RawMessage `gorm:"type:jsonb"`
	Response            json.RawMessage `gorm:"type:jsonb"`
	Usage               json.RawMessage `gorm:"type:jsonb"`
	FirstTokenLatencyMs *int64
	Status              string
	CreatedAt           time.Time
	CompletedAt         *time.Time
}

func (RunCompactionCall) TableName() string { return "bot.run_compaction_calls" }
