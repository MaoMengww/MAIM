package run

import (
	"encoding/json"
	"testing"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDecodeExecutionArgumentsRejectsModelSuppliedShape(t *testing.T) {
	valid := `{"version_id":"019b0123-4567-789a-bcde-f0123456789a","file_id":"019b0123-4567-789a-bcde-f0123456789b"}`
	if _, err := decodeExecutionArguments(valid); err != nil {
		t.Fatalf("valid exact identity rejected: %v", err)
	}
	for _, raw := range []string{
		`{"version_id":"019b0123-4567-789a-bcde-f0123456789a","file_id":"019b0123-4567-789a-bcde-f0123456789b","code":"rm -rf /"}`,
		`{"version_id":"","file_id":"019b0123-4567-789a-bcde-f0123456789b"}`,
		`{"version_id":"019b0123-4567-789a-bcde-f0123456789a"}`,
	} {
		if _, err := decodeExecutionArguments(raw); err == nil {
			t.Fatalf("unbounded execution argument accepted: %s", raw)
		}
	}
}

func TestExecutionOutcomeFailureDistinguishesQualityFromEnvironment(t *testing.T) {
	cases := []struct {
		record model.RunExecution
		code   codes.Code
	}{
		{model.RunExecution{Outcome: "output_limit"}, codes.ResourceExhausted},
		{model.RunExecution{Outcome: "timeout"}, codes.DeadlineExceeded},
		{model.RunExecution{Outcome: "stopped"}, codes.Unavailable},
		{model.RunExecution{Outcome: "failed", FailureClass: "worker_failure"}, codes.FailedPrecondition},
		{model.RunExecution{Outcome: "failed", FailureClass: "environment_error"}, codes.Unavailable},
	}
	for _, scenario := range cases {
		record := scenario.record
		err := executionOutcomeFailure(&record)
		if status.Code(err) != scenario.code {
			t.Fatalf("outcome %s/%s got %s, want %s", scenario.record.Outcome, scenario.record.FailureClass, status.Code(err), scenario.code)
		}
	}
}

func TestSanitizeEvidenceKeepsOnlyBoundedFacts(t *testing.T) {
	raw := json.RawMessage(`{"removed":true,"resources_absent":true,"stop_confirmed":true,"wait":0,"host":{"cgroup":"/sys/fs/cgroup/leak"},"stderr":"private material","host_pids":[{"pid":1}]}`)
	sanitized := sanitizeEvidence(raw)
	var value map[string]any
	if err := json.Unmarshal(sanitized, &value); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"host", "stderr", "host_pids"} {
		if _, found := value[key]; found {
			t.Fatalf("private execution evidence %q persisted", key)
		}
	}
	if value["removed"] != true || value["wait"] != float64(0) {
		t.Fatalf("bounded cleanup facts lost: %s", sanitized)
	}
}
