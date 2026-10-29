package execution

import (
	"context"
	"encoding/json"
)

// Limits mirror the manager's finite defaults. Callers submit every value
// explicitly; the manager rejects omission, zero, negatives and out-of-range
// values instead of interpreting them as unlimited.
type Limits struct {
	CPUMillis      int `json:"cpu_millis"`
	MemoryBytes    int `json:"memory_bytes"`
	PIDs           int `json:"pids"`
	WorkspaceBytes int `json:"workspace_bytes"`
	TmpBytes       int `json:"tmp_bytes"`
	TotalSeconds   int `json:"total_seconds"`
	StepSeconds    int `json:"step_seconds"`
	InputBytes     int `json:"input_bytes"`
	OutputBytes    int `json:"output_bytes"`
	OutputFiles    int `json:"output_files"`
	LogBytes       int `json:"log_bytes"`
}

// DefaultLimits is the reviewed bounded budget for the fixed CSV capability.
func DefaultLimits() Limits {
	return Limits{
		CPUMillis:      500,
		MemoryBytes:    134217728,
		PIDs:           32,
		WorkspaceBytes: 8388608,
		TmpBytes:       2097152,
		TotalSeconds:   30,
		StepSeconds:    15,
		InputBytes:     65536,
		OutputBytes:    65536,
		OutputFiles:    1,
		LogBytes:       65536,
	}
}

// SubmitRequest is the only Run workload shape the manager accepts.
type SubmitRequest struct {
	RunID       string `json:"run_id"`
	Generation  int64  `json:"generation"`
	Capability  string `json:"capability"`
	InputBase64 string `json:"input_b64"`
	Limits      Limits `json:"limits"`
}

// File is either the raw bounded artifact (normal Run) or its verified summary
// (candidate validation). Name is the fixed result name.
type File struct {
	Name    string `json:"name"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	DataB64 string `json:"data_b64,omitempty"`
}

// Result is the fixed workload envelope. Facts are observations, never
// instructions or authority.
type Result struct {
	Files []File         `json:"files"`
	Facts map[string]any `json:"facts"`
}

// Record is the manager's durable execution state. Unknown or missing cleanup
// proof must never be projected as a confirmed stop.
type Record struct {
	ExecutionID   string          `json:"execution_id"`
	RunID         string          `json:"run_id"`
	Generation    int64           `json:"generation"`
	ContainerID   *string         `json:"container_id"`
	ContainerName string          `json:"container_name"`
	Phase         string          `json:"phase"`
	Outcome       string          `json:"outcome"`
	FailureClass  string          `json:"failure_class"`
	InputSHA256   string          `json:"input_sha256"`
	ImageID       string          `json:"image_id"`
	WorkerSHA256  string          `json:"worker_sha256"`
	Operation     string          `json:"operation"`
	Error         string          `json:"error"`
	Result        *Result         `json:"result"`
	Evidence      json.RawMessage `json:"evidence"`
	StopRequested bool            `json:"stop_requested"`
}

// Removed, ResourcesAbsent and StopConfirmed are derived from Evidence so the
// caller cannot mistake "API object disappeared" for host cleanup.
func (r *Record) Cleanup() (removed, resourcesAbsent, stopConfirmed bool, wait *int) {
	var evidence struct {
		Removed         bool `json:"removed"`
		ResourcesAbsent bool `json:"resources_absent"`
		StopConfirmed   bool `json:"stop_confirmed"`
		Wait            *int `json:"wait"`
	}
	if len(r.Evidence) > 0 {
		_ = json.Unmarshal(r.Evidence, &evidence)
	}
	return evidence.Removed, evidence.ResourcesAbsent, evidence.StopConfirmed, evidence.Wait
}

func SubmitRun(ctx context.Context, request SubmitRequest) (Record, error) {
	var record Record
	err := Exchange(ctx, map[string]any{"action": "submit", "request": request}, &record)
	return record, err
}

func LookupRun(ctx context.Context, runID string, generation int64) (Record, error) {
	var record Record
	err := Exchange(ctx, map[string]any{"action": "lookup", "run_id": runID, "generation": generation}, &record)
	return record, err
}

func GetRun(ctx context.Context, executionID string) (Record, error) {
	var record Record
	err := Exchange(ctx, map[string]any{"action": "get", "execution_id": executionID}, &record)
	return record, err
}

// StopRun requests whole-container stop. A "stopping" phase is not evidence;
// callers must keep observing until the manager reports verified cleanup.
func StopRun(ctx context.Context, executionID string) (Record, error) {
	var record Record
	err := Exchange(ctx, map[string]any{"action": "stop", "execution_id": executionID}, &record)
	return record, err
}
