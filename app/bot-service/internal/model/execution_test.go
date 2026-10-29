package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPublicExecutionNeverClaimsUnconfirmedStop(t *testing.T) {
	cases := []struct {
		phase, outcome, want string
	}{
		{"running", "unknown", "running"},
		{"stopping", "stopped", "stopping"},
		{"unknown", "stopped", "unconfirmed"},
		{"cleaned", "succeeded", "succeeded"},
		{"cleaned", "output_limit", "output_limit"},
		{"cleaned", "timeout", "timeout"},
		{"cleaned", "stopped", "stopped"},
		{"cleaned", "unknown", "unconfirmed"},
	}
	for _, scenario := range cases {
		record := RunExecution{Phase: scenario.phase, Outcome: scenario.outcome, UpdatedAt: time.Unix(1, 0), Evidence: json.RawMessage(`{"removed":true,"resources_absent":true,"stop_confirmed":false}`)}
		public := record.Public()
		if public.Status != scenario.want {
			t.Fatalf("phase=%s outcome=%s got %s, want %s", scenario.phase, scenario.outcome, public.Status, scenario.want)
		}
		if public.StopConfirmed {
			t.Fatalf("missing stop proof projected as confirmed: %+v", public)
		}
	}
}
