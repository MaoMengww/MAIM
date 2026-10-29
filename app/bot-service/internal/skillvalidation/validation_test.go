package skillvalidation

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixtureIdentity() Identity {
	return Identity{Scripts: map[string]string{"worker.py": "reviewed fixture bytes\n"},
		ImageDigest: "sha256:" + strings.Repeat("a", 64), EntryPoint: entryPoint}
}

// A transport fixture exercises adapter decisions only. It is never used as
// evidence of real script admission, Docker isolation or node cleanup.
func fixtureManager(t *testing.T, handler func(map[string]json.RawMessage) any) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIM_EXECUTION_SOCKET", path)
	t.Setenv("AIM_EXECUTION_SSH_TARGET", "")
	var workers sync.WaitGroup
	workers.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer connection.Close()
				line, err := bufio.NewReader(connection).ReadBytes('\n')
				if err != nil {
					return
				}
				var request map[string]json.RawMessage
				if json.Unmarshal(line, &request) != nil {
					return
				}
				_ = json.NewEncoder(connection).Encode(map[string]any{"ok": true, "result": handler(request)})
			}()
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		workers.Wait()
	})
}

func TestFreezeRequiresExactSupportedShape(t *testing.T) {
	identity := fixtureIdentity()
	if _, err := freeze(identity); err != nil {
		t.Fatal(err)
	}
	identity.Scripts["another.py"] = "unreviewed"
	if _, err := freeze(identity); err == nil {
		t.Fatal("additional scripts admitted")
	}
	delete(identity.Scripts, "another.py")
	identity.EntryPoint += " extra"
	if _, err := freeze(identity); err == nil {
		t.Fatal("changed entry admitted")
	}
}

func TestReviewChecksManagerIdentity(t *testing.T) {
	identity := fixtureIdentity()
	frozen, _ := freeze(identity)
	fixtureManager(t, func(request map[string]json.RawMessage) any {
		return admission{frozenIdentity: frozen, ReviewDigest: strings.Repeat("b", 64), EnvironmentDigest: strings.Repeat("c", 64)}
	})
	if _, err := Review(t.Context(), identity); err == nil {
		t.Fatal("self-reported mismatched review digest accepted")
	}
}

func TestEvaluateExactArtifactAndCleanup(t *testing.T) {
	for _, scenario := range []struct {
		name, actual, want    string
		removed               bool
		outcome, failureClass string
	}{
		{"exact", "rows,total\n1,2\n", "passed", true, "succeeded", ""},
		{"different-bytes", "rows,total\r\n1,2\r\n", "failed", true, "succeeded", ""},
		{"missing-cleanup", "rows,total\n1,2\n", "environment_error", false, "succeeded", ""},
		{"runtime-failed", "", "environment_error", true, "failed", "environment_error"},
		{"legacy-unknown-failure", "", "environment_error", true, "failed", ""},
		{"runtime-stopped", "", "environment_error", true, "stopped", "environment_error"},
		{"worker-failed", "", "failed", true, "failed", "worker_failure"},
		{"worker-timeout", "", "failed", true, "timeout", "worker_failure"},
		{"worker-output-limit", "", "failed", true, "output_limit", "worker_failure"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			identity := fixtureIdentity()
			frozen, _ := freeze(identity)
			input, expected := []byte("value\n2\n"), []byte("rows,total\n1,2\n")
			key := "stable-" + scenario.name
			zero := 0
			current := record{ExecutionID: "fixture-only", RunID: digest([]byte("aim-skill-validation-v1\x00" + key)),
				ContainerID: strings.Repeat("d", 64), Phase: "cleaned", Outcome: scenario.outcome, FailureClass: scenario.failureClass,
				InputSHA256: digest(input), ImageID: frozen.ImageID, WorkerSHA256: frozen.WorkerSHA256,
				Removed: scenario.removed, ResourcesAbsent: true, StopConfirmed: true, Wait: &zero}
			current.Validation.Identity = frozen
			current.Validation.ReviewDigest = reviewDigest(frozen)
			current.Validation.EnvironmentDigest = strings.Repeat("e", 64)
			current.Validation.ExpectedSHA256 = digest(expected)
			current.Files = append(current.Files, struct {
				Name   string `json:"name"`
				SHA256 string `json:"sha256"`
				Size   int    `json:"size"`
			}{"result.csv", digest([]byte(scenario.actual)), len(scenario.actual)})
			fixtureManager(t, func(request map[string]json.RawMessage) any {
				// Lookup returns an existing durable record: there must be no submit.
				var action string
				_ = json.Unmarshal(request["action"], &action)
				if action != "validation-lookup" {
					t.Errorf("unexpected action %s", action)
				}
				return current
			})
			result, err := Evaluate(t.Context(), key, identity, Sample{InputBase64: base64.StdEncoding.EncodeToString(input),
				ExpectedOutputBase64: base64.StdEncoding.EncodeToString(expected)})
			if err != nil || result.Status != scenario.want {
				t.Fatalf("got %+v, %v; want %s", result, err, scenario.want)
			}
			if result.Terminal != scenario.removed {
				t.Fatalf("terminal=%v, want cleanup-proven terminal=%v", result.Terminal, scenario.removed)
			}
		})
	}
}

func TestEvaluateMissingEvidenceDoesNotConnect(t *testing.T) {
	t.Setenv("AIM_EXECUTION_SOCKET", "")
	t.Setenv("AIM_EXECUTION_SSH_TARGET", "")
	result, err := Evaluate(t.Context(), "missing-evidence", fixtureIdentity(), Sample{})
	if err != nil || result.Status != "insufficient_evidence" || !result.Terminal {
		t.Fatalf("got %+v, %v", result, err)
	}
}

func TestEvaluateUnknownReconcilesSameExecutionWithoutSubmit(t *testing.T) {
	identity := fixtureIdentity()
	frozen, _ := freeze(identity)
	input, expected := []byte("value\n2\n"), []byte("rows,total\n1,2\n")
	key := "stable-unknown-reconciliation"
	zero := 0
	current := record{ExecutionID: "fixture-durable", RunID: digest([]byte("aim-skill-validation-v1\x00" + key)),
		ContainerID: strings.Repeat("d", 64), Phase: "unknown", Outcome: "succeeded",
		InputSHA256: digest(input), ImageID: frozen.ImageID, WorkerSHA256: frozen.WorkerSHA256,
		Removed: true, ResourcesAbsent: true, StopConfirmed: true, Wait: &zero}
	current.Validation.Identity = frozen
	current.Validation.ReviewDigest = reviewDigest(frozen)
	current.Validation.EnvironmentDigest = strings.Repeat("e", 64)
	current.Validation.ExpectedSHA256 = digest(expected)
	current.Files = append(current.Files, struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Size   int    `json:"size"`
	}{"result.csv", digest(expected), len(expected)})
	lookups := 0
	fixtureManager(t, func(request map[string]json.RawMessage) any {
		var action string
		_ = json.Unmarshal(request["action"], &action)
		switch action {
		case "validation-lookup":
			lookups++
			if lookups > 1 {
				current.Phase = "cleaned"
			}
		case "validation-stop":
		default:
			t.Errorf("unknown execution replayed through %s", action)
		}
		return current
	})
	sample := Sample{InputBase64: base64.StdEncoding.EncodeToString(input), ExpectedOutputBase64: base64.StdEncoding.EncodeToString(expected)}
	first, err := Evaluate(t.Context(), key, identity, sample)
	if err != nil || first.Terminal || first.Status != "environment_error" {
		t.Fatalf("unknown execution became terminal: %+v, %v", first, err)
	}
	second, err := Evaluate(t.Context(), key, identity, sample)
	if err != nil || !second.Terminal || second.Status != "passed" || second.ExecutionID != first.ExecutionID {
		t.Fatalf("same-key reconciliation lost execution identity: %+v, %v", second, err)
	}
}
