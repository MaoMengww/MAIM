package skill

import (
	"encoding/json"
	"time"
)

// Frozen is the durable identity of one skill version attached to an accepted
// Run. It deliberately carries no permission: reads check current Bot and
// enablement state at each access boundary.
type Frozen struct {
	VersionID   string `json:"version_id"`
	PackageID   string `json:"package_id"`
	PublisherID string `json:"publisher_id"`
	ScopeID     string `json:"scope_id"`
	ScopeKey    string `json:"scope_key,omitempty"`
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Digest      string `json:"digest"`
	Explicit    bool   `json:"explicit"`
	// Executable marks a published fixed-script capability whose exact script,
	// image and entry passed trusted human review and validation. It is not an
	// execution permit; each invocation rechecks the current manager policy.
	Executable bool `json:"executable,omitempty"`
}

// FrozenSkills is the public Run projection. Loaded state is derived from the
// Run's durable skill loads, never from framework memory.
type FrozenSkill struct {
	VersionID   string `json:"version_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Digest      string `json:"digest"`
	Version     int    `json:"version"`
	Explicit    bool   `json:"explicit"`
	Loaded      bool   `json:"loaded"`
}

// Load records progressive disclosure in the Run's durable trajectory.
type Load struct {
	VersionID string `json:"version_id"`
	Resource  string `json:"resource"`
	Digest    string `json:"digest"`
	Bytes     int    `json:"bytes"`
}

type EvaluationView struct {
	Status         string `json:"status"`
	Passed         bool   `json:"passed"`
	Reason         string `json:"reason"`
	Digest         string `json:"digest"`
	TaskDigest     string `json:"task_digest"`
	ExpectedDigest string `json:"expected_digest"`
	OutputDigest   string `json:"output_digest"`
	ModelVersion   string `json:"model_version"`
	CreatedAt      int64  `json:"created_at"`
}

type CandidateView struct {
	ID                 string          `json:"id"`
	SkillID            string          `json:"skill_id"`
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	Digest             string          `json:"digest"`
	License            string          `json:"license"`
	Status             string          `json:"status"`
	Failure            string          `json:"failure,omitempty"`
	BaselineVersionID  *string         `json:"baseline_version_id,omitempty"`
	PublishedVersionID *string         `json:"published_version_id,omitempty"`
	ApprovedDigest     string          `json:"approved_digest,omitempty"`
	Evaluation         *EvaluationView `json:"evaluation,omitempty"`
	CreatedAt          int64           `json:"created_at"`
}

type VersionView struct {
	VersionID   string `json:"version_id"`
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Digest      string `json:"digest"`
	Status      string `json:"status"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"created_at"`
}

type PackageView struct {
	SkillID         string            `json:"skill_id"`
	Name            string            `json:"name"`
	License         string            `json:"license"`
	PublisherID     string            `json:"publisher_id"`
	ScopeKey        string            `json:"scope_key"`
	BindingRevision int64             `json:"binding_revision"`
	BlockedUpgrade  *BlockedUpgrade   `json:"blocked_upgrade,omitempty"`
	RollbackHold    bool              `json:"rollback_hold"`
	CanManage       bool              `json:"can_manage"`
	Activities      []VersionActivity `json:"activities"`
	EnabledVersion  *VersionView      `json:"enabled_version,omitempty"`
	Versions        []VersionView     `json:"versions"`
	Candidates      []CandidateView   `json:"candidates"`
}

type AvailableSkill struct {
	VersionID   string `json:"version_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
}

type Package struct {
	Name         string
	Description  string
	License      string
	Instructions string
	Resources    map[string]string
	Scripts      map[string]string
	ImageDigest  string
	EntryPoint   string
	Digest       string
}

type importRequest struct {
	Archive            string `json:"archive"`
	SubmissionKey      string `json:"submission_key"`
	RepresentativeTask string `json:"representative_task"`
	ExpectedOutput     string `json:"expected_output"`
}

type candidate struct {
	ID                           string          `gorm:"column:id;primaryKey;type:uuid"`
	SkillID                      string          `gorm:"column:skill_id;type:uuid"`
	BotID                        string          `gorm:"column:bot_id;type:uuid"`
	Digest                       string          `gorm:"column:digest"`
	Name                         string          `gorm:"column:name"`
	Description                  string          `gorm:"column:description"`
	Instructions                 string          `gorm:"column:instructions"`
	Resources                    json.RawMessage `gorm:"column:resources;type:jsonb"`
	License                      string          `gorm:"column:license"`
	BaselineVersionID            *string         `gorm:"column:baseline_version_id;type:uuid"`
	Status                       string          `gorm:"column:status"`
	Failure                      string          `gorm:"column:failure"`
	Task                         string          `gorm:"column:task"`
	ExpectedOutput               string          `gorm:"column:expected_output"`
	EvaluationDigest             string          `gorm:"column:evaluation_digest"`
	ApprovedDigest               string          `gorm:"column:approved_digest"`
	ApprovedEvaluationDigest     string          `gorm:"column:approved_evaluation_digest"`
	ApprovedBy                   *string         `gorm:"column:approved_by;type:uuid"`
	ApprovedAuthorizationVersion int64           `gorm:"column:approved_authorization_version"`
	PublishedVersionID           *string         `gorm:"column:published_version_id;type:uuid"`
	CreatedAt                    time.Time       `gorm:"column:created_at"`
	UpdatedAt                    time.Time       `gorm:"column:updated_at"`
}

func (candidate) TableName() string { return "bot.skill_candidates" }

type version struct {
	ID                 string          `gorm:"column:id;primaryKey;type:uuid"`
	SkillID            string          `gorm:"column:skill_id;type:uuid"`
	BotID              string          `gorm:"column:bot_id;type:uuid"`
	Version            int             `gorm:"column:version"`
	Digest             string          `gorm:"column:digest"`
	Name               string          `gorm:"column:name"`
	Description        string          `gorm:"column:description"`
	Instructions       string          `gorm:"column:instructions"`
	Resources          json.RawMessage `gorm:"column:resources;type:jsonb"`
	License            string          `gorm:"column:license"`
	ScopeKey           string          `gorm:"column:scope_key"`
	ValidationContract json.RawMessage `gorm:"column:validation_contract;type:jsonb"`
	Status             string          `gorm:"column:status"`
	CreatedAt          time.Time       `gorm:"column:created_at"`
}

func (version) TableName() string { return "bot.skill_versions" }

type packageRow struct {
	ID          string    `gorm:"column:id;primaryKey;type:uuid"`
	BotID       string    `gorm:"column:bot_id;type:uuid"`
	PublisherID string    `gorm:"column:publisher_id;type:uuid"`
	Name        string    `gorm:"column:name"`
	License     string    `gorm:"column:license"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (packageRow) TableName() string { return "bot.skill_packages" }

type binding struct {
	ID                  string          `gorm:"column:id;primaryKey;type:uuid"`
	BotID               string          `gorm:"column:bot_id;type:uuid"`
	SkillID             string          `gorm:"column:skill_id;type:uuid"`
	VersionID           string          `gorm:"column:version_id;type:uuid"`
	ScopeKey            string          `gorm:"column:scope_key"`
	Enabled             bool            `gorm:"column:enabled"`
	Revision            int64           `gorm:"column:revision"`
	RollbackHoldVersion int             `gorm:"column:rollback_hold_version"`
	BlockedVersionID    *string         `gorm:"column:blocked_version_id;type:uuid"`
	BlockedReasons      json.RawMessage `gorm:"column:blocked_reasons;type:jsonb"`
	UpdatedAt           time.Time       `gorm:"column:updated_at"`
}

func (binding) TableName() string { return "bot.skill_bindings" }

type evaluation struct {
	ID            string    `gorm:"column:id;primaryKey;type:uuid"`
	CandidateID   string    `gorm:"column:candidate_id;type:uuid"`
	BotID         string    `gorm:"column:bot_id;type:uuid"`
	Digest        string    `gorm:"column:digest"`
	ContentDigest string    `gorm:"column:content_digest"`
	Status        string    `gorm:"column:status"`
	Reason        string    `gorm:"column:reason"`
	TaskDigest    string    `gorm:"column:task_digest"`
	ExpectedHash  string    `gorm:"column:expected_digest"`
	OutputDigest  string    `gorm:"column:output_digest"`
	Observed      string    `gorm:"column:observed"`
	ModelVersion  string    `gorm:"column:model_version"`
	Attempt       int       `gorm:"column:attempt"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (evaluation) TableName() string { return "bot.skill_evaluations" }

type operationRow struct {
	ID            string          `gorm:"column:id;primaryKey;type:uuid"`
	BotID         string          `gorm:"column:bot_id;type:uuid"`
	Operation     string          `gorm:"column:operation"`
	SubmissionKey string          `gorm:"column:submission_key"`
	RequestHash   string          `gorm:"column:request_hash"`
	Response      json.RawMessage `gorm:"column:response;type:jsonb"`
	CreatedAt     time.Time       `gorm:"column:created_at"`
}

func (operationRow) TableName() string { return "bot.skill_operations" }
