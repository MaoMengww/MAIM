package model

import (
	"encoding/json"
	"time"
)

// Run owns work facts. Private snapshots and trajectory are never serialized publicly.
type Run struct {
	ID                          string  `gorm:"type:uuid;primaryKey"`
	InteractionID               string  `gorm:"type:uuid"`
	ParentRunID                 *string `gorm:"type:uuid"`
	InteractionStatus           string
	BotID                       string            `gorm:"type:uuid"`
	ConvID                      string            `gorm:"type:uuid"`
	BindingID                   string            `gorm:"type:uuid"`
	RequesterID                 string            `gorm:"type:uuid"`
	RequesterMemberID           *string           `gorm:"type:uuid"`
	ControllerID                string            `gorm:"type:uuid"`
	ControllerMemberID          *string           `gorm:"type:uuid"`
	ControlUserIDs              []string          `gorm:"type:jsonb;serializer:json"`
	ControlMemberships          map[string]string `gorm:"type:jsonb;serializer:json"`
	StopStatus                  string
	StreamStatus                string
	StopReason                  string
	StopRequestedAt             *time.Time
	AccessRevoked               bool
	BotAuthorizationVersion     int64
	BindingAuthorizationVersion int64
	ViewerCanControl            *bool `gorm:"-"`
	InputKey                    string
	InputHash                   string
	SourceMessageID             *string `gorm:"type:uuid"`
	ReplyToMsgID                *string `gorm:"type:uuid"`
	HistoryGrantID              *string `gorm:"type:uuid"`
	InputVersion                int64
	AppliedInputVersion         int64
	InputSources                []InputSource `gorm:"type:jsonb;serializer:json"`
	Version                     int64
	TrajectoryVersion           int64
	ExecutionGeneration         int64
	ExecutorType                string
	ExecutorCapabilities        ExecutorCapabilities `gorm:"type:jsonb;serializer:json"`
	DeliveryStatus              string
	ExecutorStatus              string
	ExecutorSequence            int64
	ControlOperation            string
	TriggerKind                 string
	Status                      string
	Stage                       string
	WaitingReason               string
	ResultText                  string
	ResultType                  string
	ResultContent               json.RawMessage `gorm:"type:jsonb"`
	OutputStatus                string
	MessageID                   *string `gorm:"type:uuid"`
	Error                       string
	Config                      json.RawMessage `gorm:"type:jsonb"`
	Sources                     json.RawMessage `gorm:"type:jsonb"`
	Materials                   json.RawMessage `gorm:"type:jsonb"`
	Steps                       []RunStep       `gorm:"type:jsonb;serializer:json"`
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

func (Run) TableName() string { return "bot.runs" }

type RunStep struct {
	Version   int64  `json:"version"`
	Stage     string `json:"stage"`
	CreatedAt int64  `json:"created_at"`
}

type ExecutorCapabilities struct {
	InputTypes     []string `json:"input_types"`
	SupportsInput  bool     `json:"supports_input"`
	SupportsStop   bool     `json:"supports_stop"`
	SupportsResume bool     `json:"supports_resume"`
}
type InputSource struct {
	Version         int64    `json:"version"`
	RequesterID     string   `json:"requester_id"`
	SourceMessageID *string  `json:"source_message_id,omitempty"`
	SenderType      string   `json:"sender_type"`
	MessageIDs      []string `json:"message_ids"`
	FileIDs         []string `json:"file_ids"`
	HistoryGrantID  *string  `json:"history_grant_id,omitempty"`
	ParentRunID     *string  `json:"parent_run_id,omitempty"`
}

type RunReceipt struct {
	ID          string `gorm:"type:uuid;primaryKey"`
	RunID       string `gorm:"type:uuid"`
	UpdateKey   string
	ContentHash string
	Receipt     json.RawMessage `gorm:"type:jsonb"`
	CreatedAt   time.Time
}

func (RunReceipt) TableName() string { return "bot.run_receipts" }

type PublicRun struct {
	ID                   string               `json:"id"`
	InteractionID        string               `json:"interaction_id"`
	ParentRunID          *string              `json:"parent_run_id,omitempty"`
	InteractionStatus    string               `json:"interaction_status"`
	BotID                string               `json:"bot_id"`
	ConvID               string               `json:"conv_id"`
	BindingID            string               `json:"binding_id"`
	RequesterID          string               `json:"requester_id"`
	ControllerID         string               `json:"controller_id"`
	ControlUserIDs       []string             `json:"control_user_ids"`
	StopStatus           string               `json:"stop_status"`
	StreamStatus         string               `json:"stream_status"`
	StopReason           string               `json:"stop_reason"`
	CanControl           *bool                `json:"can_control,omitempty"`
	InputKey             string               `json:"input_key"`
	InputSources         []InputSource        `json:"input_sources"`
	SourceMessageID      *string              `json:"source_message_id,omitempty"`
	InputVersion         int64                `json:"input_version"`
	Version              int64                `json:"version"`
	ExecutorType         string               `json:"executor_type"`
	ExecutorCapabilities ExecutorCapabilities `json:"executor_capabilities"`
	DeliveryStatus       string               `json:"delivery_status"`
	ExecutorStatus       string               `json:"executor_status"`
	Generation           int64                `json:"generation"`
	TriggerKind          string               `json:"trigger_kind"`
	Status               string               `json:"status"`
	Stage                string               `json:"stage"`
	WaitingReason        string               `json:"waiting_reason"`
	ResultText           string               `json:"result_text"`
	ResultType           string               `json:"result_type"`
	ResultContent        json.RawMessage      `json:"result_content,omitempty"`
	OutputStatus         string               `json:"output_status"`
	MessageID            *string              `json:"message_id,omitempty"`
	Error                string               `json:"error,omitempty"`
	Sources              []map[string]string  `json:"sources"`
	Steps                []RunStep            `json:"steps"`
	CreatedAt            int64                `json:"created_at"`
	UpdatedAt            int64                `json:"updated_at"`
}

func (r *Run) Public() PublicRun {
	// Explicit field allowlisting prevents private tool contents and credentials
	// from leaking as the stored execution schema grows.
	sources := make([]map[string]string, 0)
	var private []map[string]any
	_ = json.Unmarshal(r.Sources, &private)
	for _, source := range private {
		public := make(map[string]string)
		for _, key := range []string{"type", "kind", "kb_name", "kb_id", "doc_id", "chunk_id", "title", "message_id", "file_id", "parent_run_id", "requester_id", "history_grant_id", "sender_type", "sender_id", "tool_name"} {
			if value, ok := source[key].(string); ok {
				public[key] = value
			}
		}
		sources = append(sources, public)
	}
	steps := r.Steps
	if steps == nil {
		steps = []RunStep{}
	}
	controllers := r.ControlUserIDs
	if controllers == nil {
		controllers = []string{}
	}
	return PublicRun{ID: r.ID, InteractionID: r.InteractionID, BotID: r.BotID, ConvID: r.ConvID,
		ParentRunID: r.ParentRunID, InteractionStatus: r.InteractionStatus, WaitingReason: r.WaitingReason,
		BindingID: r.BindingID, RequesterID: r.RequesterID, InputKey: r.InputKey, SourceMessageID: r.SourceMessageID,
		ControllerID: r.ControllerID, ControlUserIDs: controllers, StopStatus: r.StopStatus, StreamStatus: r.StreamStatus, StopReason: r.StopReason,
		CanControl:   r.ViewerCanControl,
		InputVersion: r.InputVersion, Version: r.Version, Status: r.Status, Stage: r.Stage,
		InputSources: r.InputSources,
		ExecutorType: r.ExecutorType, ExecutorCapabilities: r.ExecutorCapabilities, DeliveryStatus: r.DeliveryStatus, ExecutorStatus: r.ExecutorStatus, Generation: r.ExecutionGeneration, TriggerKind: r.TriggerKind, ResultType: r.ResultType, ResultContent: r.ResultContent,
		ResultText: r.ResultText, OutputStatus: r.OutputStatus, MessageID: r.MessageID, Error: r.Error,
		Sources: sources, Steps: steps, CreatedAt: r.CreatedAt.Unix(), UpdatedAt: r.UpdatedAt.Unix()}
}

type RunInput struct {
	ID              string `gorm:"type:uuid;primaryKey"`
	RunID           string `gorm:"type:uuid"`
	Version         int64
	RequesterID     string  `gorm:"type:uuid"`
	SourceMessageID *string `gorm:"type:uuid"`
	InputKey        string
	Content         string
	ContentHash     string
	Materials       json.RawMessage `gorm:"type:jsonb"`
	Sources         json.RawMessage `gorm:"type:jsonb"`
	CreatedAt       time.Time
}

func (RunInput) TableName() string { return "bot.run_inputs" }

type Interaction struct {
	ID          string `gorm:"type:uuid;primaryKey"`
	BotID       string `gorm:"type:uuid"`
	ConvID      string `gorm:"type:uuid"`
	BindingID   string `gorm:"type:uuid"`
	RequesterID string `gorm:"type:uuid"`
	Status      string
	ClosedAt    *time.Time
	CreatedAt   time.Time
}

func (Interaction) TableName() string { return "bot.interactions" }

type RunTrajectory struct {
	ID        string `gorm:"type:uuid;primaryKey"`
	RunID     string `gorm:"type:uuid"`
	Version   int64
	Kind      string
	Block     json.RawMessage `gorm:"type:jsonb"`
	CreatedAt time.Time
}

func (RunTrajectory) TableName() string { return "bot.run_trajectory" }

type RunModelCall struct {
	ID                  string `gorm:"type:uuid;primaryKey"`
	RunID               string `gorm:"type:uuid"`
	InteractionID       string `gorm:"type:uuid"`
	InputUpperBound     int64
	TrajectoryVersion   int64
	ExecutionGeneration int64
	BranchVersion       int64
	AssemblyVersion     string
	Request             json.RawMessage `gorm:"type:jsonb"`
	Response            json.RawMessage `gorm:"type:jsonb"`
	Usage               json.RawMessage `gorm:"type:jsonb"`
	FirstTokenLatencyMs *int64
	Status              string
	CreatedAt           time.Time
	CompletedAt         *time.Time
}

func (RunModelCall) TableName() string { return "bot.run_model_calls" }

type RunOutbox struct {
	ID                  string `gorm:"type:uuid;primaryKey"`
	RunID               string `gorm:"type:uuid"`
	Kind                string
	Version             int64
	Payload             json.RawMessage `gorm:"type:jsonb"`
	Attempts            int
	AvailableAt         time.Time
	LeaseUntil          *time.Time
	LeaseToken          *string `gorm:"type:uuid"`
	ExecutionGeneration int64   `gorm:"-"` // Captured only by Claim; never read from a newer worker.
	CompletedAt         *time.Time
	CreatedAt           time.Time
}

func (RunOutbox) TableName() string { return "bot.run_outbox" }
