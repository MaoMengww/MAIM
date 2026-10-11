package model

import (
	"encoding/json"
	"time"
)

// RunAction is an executor-private, exact-parameter authorization and execution receipt.
// Credentials are rejected at admission; raw results are never projected publicly.
type RunAction struct {
	ID                   string `gorm:"primaryKey;type:uuid"`
	RunID                string `gorm:"type:uuid"`
	BotID                string `gorm:"type:uuid"`
	BindingID            string `gorm:"type:uuid"`
	RequesterID          string `gorm:"type:uuid;serializer:nullable_uuid"`
	ControllerID         string `gorm:"type:uuid"`
	ControllerMemberID   string `gorm:"type:uuid"`
	ServerID             string
	ToolName             string
	DefinitionHash       string
	ParametersHash       string
	Arguments            json.RawMessage `gorm:"type:jsonb"`
	ToolDefinition       json.RawMessage `gorm:"type:jsonb"`
	Provenance           json.RawMessage `gorm:"type:jsonb"`
	SourceInvalidated    bool
	Target               string
	Risk                 string
	SubmissionKey        string
	Generation           int64
	InputVersion         int64
	Status               string
	ApproverID           string
	GrantID              *string `gorm:"type:uuid"`
	ExpiresAt            time.Time
	ConfirmationSharedAt *time.Time
	ConfirmationOwnerID  *string `gorm:"type:uuid"`
	ClaimedAt            *time.Time
	CompletedAt          *time.Time
	Result               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (RunAction) TableName() string { return "run_actions" }

type ActionGrant struct {
	ID                          string `gorm:"primaryKey;type:uuid"`
	BotID                       string `gorm:"type:uuid"`
	ConvID                      string `gorm:"type:uuid"`
	BindingID                   string `gorm:"type:uuid"`
	OwnerID                     string `gorm:"type:uuid"`
	RequesterID                 string `gorm:"type:uuid"`
	SubjectMemberID             string `gorm:"type:uuid"`
	ServerID                    string
	ToolName                    string
	DefinitionHash              string
	ParametersHash              string
	BotAuthorizationVersion     int64
	BindingAuthorizationVersion int64
	ExpiresAt                   time.Time
	RevokedAt                   *time.Time
	CreatedAt                   time.Time
}

func (ActionGrant) TableName() string { return "action_grants" }

type PublicAction struct {
	ID                   string          `json:"id"`
	RunID                string          `json:"run_id"`
	ToolName             string          `json:"tool_name"`
	ServerID             string          `json:"server_id"`
	Risk                 string          `json:"risk"`
	Target               string          `json:"target"`
	Parameters           json.RawMessage `json:"parameters"`
	ParametersJSON       string          `json:"parameters_json"`
	Status               string          `json:"status"`
	ExpiresAt            int64           `json:"expires_at"`
	Generation           int64           `json:"generation"`
	InputVersion         int64           `json:"input_version"`
	CanApprove           bool            `json:"can_approve"`
	ConfirmationShared   bool            `json:"confirmation_shared"`
	CanShareConfirmation bool            `json:"can_share_confirmation"`
	Result               string          `json:"result,omitempty"`
}
