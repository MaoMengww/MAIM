package run

import (
	"encoding/json"
	"time"
)

// Review and candidate data never enter the shared Run activity stream.
type runReview struct {
	ID             string          `gorm:"type:uuid;primaryKey" json:"id"`
	RunID          string          `gorm:"type:uuid" json:"run_id"`
	BotID          string          `gorm:"type:uuid" json:"bot_id"`
	ConvID         string          `gorm:"type:uuid" json:"conv_id"`
	BindingID      string          `gorm:"type:uuid" json:"-"`
	ScopeKey       string          `json:"scope_key"`
	PersonalUserID *string         `gorm:"type:uuid" json:"-"`
	PublisherID    *string         `gorm:"type:uuid" json:"-"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason"`
	Assessment     json.RawMessage `gorm:"type:jsonb" json:"-"`
	ModelCalls     int             `json:"model_calls"`
	ModelVersion   string          `json:"model_version"`
	CandidateID    *string         `gorm:"type:uuid" json:"candidate_id,omitempty"`
	Evidence       json.RawMessage `gorm:"type:jsonb" json:"-"`
	EvidenceDigest string          `json:"-"`
	ExpiresAt      time.Time       `json:"-"`
	StartedAt      *time.Time      `json:"-"`
	CreatedAt      time.Time       `json:"-"`
	UpdatedAt      time.Time       `json:"-"`
}

func (runReview) TableName() string { return "bot.run_reviews" }

type reviewPolicy struct {
	BotID          string  `gorm:"type:uuid;primaryKey"`
	ScopeKey       string  `gorm:"primaryKey"`
	BindingID      string  `gorm:"type:uuid"`
	PersonalUserID *string `gorm:"type:uuid"`
	Enabled        bool
	Revision       int64
	UpdatedBy      string `gorm:"type:uuid"`
	UpdatedAt      time.Time
}

func (reviewPolicy) TableName() string { return "bot.run_review_policies" }

type reviewScopeRequest struct {
	ConvID        string `json:"conv_id"`
	Visibility    string `json:"visibility"`
	Enabled       *bool  `json:"enabled"`
	SubmissionKey string `json:"submission_key"`
	ReviewID      string `json:"review_id"`
}

type reviewAssessment struct {
	Valuable        bool   `json:"valuable"`
	Reason          string `json:"reason"`
	TargetVersionID string `json:"target_version_id"`
	TargetGoal      string `json:"target_goal"`
	Applicability   string `json:"applicability"`
}

type reviewExtraction struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	Instructions       string            `json:"instructions"`
	Resources          map[string]string `json:"resources"`
	Scripts            map[string]string `json:"scripts"`
	ImageDigest        string            `json:"image_digest"`
	EntryPoint         string            `json:"entry_point"`
	TargetGoal         string            `json:"target_goal"`
	Applicability      string            `json:"applicability"`
	Rationale          string            `json:"rationale"`
	RepresentativeTask string            `json:"representative_task"`
	ExpectedOutput     string            `json:"expected_output"`
}

type reviewCandidate struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	SkillID              string            `json:"skill_id"`
	SourceRunID          string            `json:"source_run_id"`
	PublisherID          string            `json:"publisher_id"`
	ScopeKey             string            `json:"scope_key"`
	Intent               string            `json:"intent"`
	TargetGoal           string            `json:"target_goal"`
	Applicability        string            `json:"applicability"`
	Rationale            string            `json:"rationale"`
	Instructions         string            `json:"instructions"`
	BaselineInstructions string            `json:"baseline_instructions"`
	Resources            map[string]string `json:"resources"`
	Scripts              map[string]string `json:"scripts"`
	ImageDigest          string            `json:"image_digest"`
	EntryPoint           string            `json:"entry_point"`
	Digest               string            `json:"digest"`
	BaselineVersionID    *string           `json:"baseline_version_id,omitempty"`
	Status               string            `json:"status"`
	CodeReviewRequired   bool              `json:"code_review_required"`
	EvidenceDigest       string            `json:"evidence_digest"`
	ExpiresAt            int64             `json:"expires_at"`
}

type reviewView struct {
	ID           string           `json:"id"`
	RunID        string           `json:"run_id"`
	BotID        string           `json:"bot_id"`
	ConvID       string           `json:"conv_id"`
	ScopeKey     string           `json:"scope_key"`
	Status       string           `json:"status"`
	Reason       string           `json:"reason"`
	ModelCalls   int              `json:"model_calls"`
	ModelVersion string           `json:"model_version"`
	CandidateID  *string          `json:"candidate_id,omitempty"`
	Candidate    *reviewCandidate `json:"candidate,omitempty"`
	CreatedAt    int64            `json:"created_at"`
	UpdatedAt    int64            `json:"updated_at"`
	ExpiresAt    int64            `json:"expires_at"`
}
