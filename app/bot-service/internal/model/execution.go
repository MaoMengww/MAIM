package model

import (
	"encoding/json"
	"time"
)

// RunExecution binds a Run to one fixed workload and trusted manager record.
// Generation is the workload's original identity, not the current worker lease.
// Persisting intent before create lets recovery reconcile without resubmitting.
type RunExecution struct {
	ID             string `gorm:"primaryKey;type:uuid"`
	RunID          string `gorm:"type:uuid"`
	Generation     int64  `gorm:"column:generation"`
	InputVersion   int64
	SkillVersionID string `gorm:"type:uuid"`
	SkillDigest    string
	InputFileID    string `gorm:"type:uuid"`
	InputSHA256    string
	InputBytes     int64
	ImageID        string
	WorkerSHA256   string
	EntryPoint     string
	ReviewDigest   string
	ExecutionID    *string `gorm:"type:uuid"`
	ContainerID    *string
	Phase          string
	Outcome        string
	FailureClass   string
	OutputSHA256   string
	OutputBytes    int64
	ResultFileID   *string         `gorm:"type:uuid"`
	ResultGrantID  *string         `gorm:"type:uuid"`
	Evidence       json.RawMessage `gorm:"type:jsonb"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (RunExecution) TableName() string { return "bot.run_executions" }

// PublicExecution explains a file-processing execution without exposing host
// paths, stdout, stderr, container addresses or raw execution output.
type PublicExecution struct {
	Status          string `json:"status"`
	Outcome         string `json:"outcome"`
	FailureClass    string `json:"failure_class,omitempty"`
	SkillVersionID  string `json:"skill_version_id"`
	InputFileID     string `json:"input_file_id"`
	ExecutionID     string `json:"execution_id,omitempty"`
	ContainerID     string `json:"container_id,omitempty"`
	InputSHA256     string `json:"input_sha256"`
	ImageID         string `json:"image_id"`
	WorkerSHA256    string `json:"worker_sha256"`
	EntryPoint      string `json:"entry_point"`
	OutputSHA256    string `json:"output_sha256,omitempty"`
	OutputBytes     int64  `json:"output_bytes,omitzero"`
	ResultFileID    string `json:"result_file_id,omitempty"`
	StopConfirmed   bool   `json:"stop_confirmed"`
	Removed         bool   `json:"removed"`
	ResourcesAbsent bool   `json:"resources_absent"`
	UpdatedAt       int64  `json:"updated_at"`
}

// Public maps the durable execution fact to a bounded viewer projection. The
// status is never derived from a closed stream or an API object disappearing.
func (e *RunExecution) Public() *PublicExecution {
	if e == nil {
		return nil
	}
	status := e.Phase
	switch e.Phase {
	case "cleaned":
		switch e.Outcome {
		case "succeeded":
			status = "succeeded"
		case "output_limit":
			status = "output_limit"
		case "timeout":
			status = "timeout"
		case "stopped":
			status = "stopped"
		case "failed":
			status = "failed"
		default:
			status = "unconfirmed"
		}
	case "unknown":
		status = "unconfirmed"
	}
	public := &PublicExecution{
		Status: status, Outcome: e.Outcome, FailureClass: e.FailureClass,
		SkillVersionID: e.SkillVersionID, InputFileID: e.InputFileID,
		InputSHA256: e.InputSHA256, ImageID: e.ImageID, WorkerSHA256: e.WorkerSHA256,
		EntryPoint: e.EntryPoint, OutputSHA256: e.OutputSHA256, OutputBytes: e.OutputBytes,
		UpdatedAt: e.UpdatedAt.Unix(),
	}
	if e.ExecutionID != nil {
		public.ExecutionID = *e.ExecutionID
	}
	if e.ContainerID != nil {
		public.ContainerID = *e.ContainerID
	}
	if e.ResultFileID != nil {
		public.ResultFileID = *e.ResultFileID
	}
	var evidence struct {
		Removed         bool `json:"removed"`
		ResourcesAbsent bool `json:"resources_absent"`
		StopConfirmed   bool `json:"stop_confirmed"`
	}
	if len(e.Evidence) > 0 {
		_ = json.Unmarshal(e.Evidence, &evidence)
	}
	public.Removed, public.ResourcesAbsent, public.StopConfirmed = evidence.Removed, evidence.ResourcesAbsent, evidence.StopConfirmed
	return public
}
