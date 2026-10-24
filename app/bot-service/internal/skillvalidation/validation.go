// Package skillvalidation admits only the node's human-reviewed fixed CSV worker.
// It never uploads code for execution or exposes Docker credentials to the caller.
package skillvalidation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	entryPoint  = "python3 -I -B /opt/aim/worker.py"
	maxSample   = 65536
	maxRequest  = 100000
	maxResponse = 1048576
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var targetPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@-]{0,252}$`)

// Identity freezes the exact UTF-8 source bytes, immutable image and fixed entry.
type Identity struct {
	Scripts     map[string]string `json:"scripts"`
	ImageDigest string            `json:"image_digest"`
	EntryPoint  string            `json:"entry_point"`
}

// Sample contains authorized exact input and expected result.csv bytes.
type Sample struct {
	InputBase64          string
	ExpectedOutputBase64 string
}

// Result deliberately contains no host paths, stdout, stderr or raw evidence.
// Status is passed, failed, insufficient_evidence or environment_error.
type Result struct {
	Status string
	// Terminal is true only for rejected evidence or a durable, cleanup-proven
	// execution. False requires reconciliation under the same execution key.
	Terminal          bool
	Reason            string
	EnvironmentDigest string
	OutputDigest      string
	ExecutionID       string
}

type frozenIdentity struct {
	WorkerSHA256 string `json:"worker_sha256"`
	ImageID      string `json:"image_id"`
	EntryPoint   string `json:"entry_point"`
}

type admission struct {
	frozenIdentity
	ReviewDigest      string `json:"review_digest"`
	EnvironmentDigest string `json:"environment_digest"`
}

type record struct {
	ExecutionID     string `json:"execution_id"`
	RunID           string `json:"run_id"`
	ContainerID     string `json:"container_id"`
	Phase           string `json:"phase"`
	Outcome         string `json:"outcome"`
	FailureClass    string `json:"failure_class"`
	InputSHA256     string `json:"input_sha256"`
	ImageID         string `json:"image_id"`
	WorkerSHA256    string `json:"worker_sha256"`
	Removed         bool   `json:"removed"`
	ResourcesAbsent bool   `json:"resources_absent"`
	StopConfirmed   bool   `json:"stop_confirmed"`
	Wait            *int   `json:"wait"`
	Validation      struct {
		Identity          frozenIdentity `json:"identity"`
		ReviewDigest      string         `json:"review_digest"`
		EnvironmentDigest string         `json:"environment_digest"`
		ExpectedSHA256    string         `json:"expected_sha256"`
	} `json:"validation"`
	Files []struct {
		Name       string `json:"name"`
		DataBase64 string `json:"data_b64"`
	} `json:"files"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func freeze(identity Identity) (frozenIdentity, error) {
	worker, found := identity.Scripts["worker.py"]
	if len(identity.Scripts) != 1 || !found || len(worker) == 0 || len(worker) > maxSample ||
		identity.EntryPoint != entryPoint || !strings.HasPrefix(identity.ImageDigest, "sha256:") ||
		!digestPattern.MatchString(strings.TrimPrefix(identity.ImageDigest, "sha256:")) {
		return frozenIdentity{}, errors.New("only the exact human-reviewed worker.py, pinned image and fixed CSV entry are supported")
	}
	return frozenIdentity{WorkerSHA256: digest([]byte(worker)), ImageID: identity.ImageDigest, EntryPoint: identity.EntryPoint}, nil
}

func reviewDigest(identity frozenIdentity) string {
	// encoding/json sorts map keys; this matches the manager's canonical JSON.
	encoded, _ := json.Marshal(map[string]any{"schema": 1, "capability": "csv-v1", "worker_sha256": identity.WorkerSHA256,
		"image_id": identity.ImageID, "entry_point": identity.EntryPoint})
	return digest(encoded)
}

// Review checks the candidate against the trusted node's root-installed human policy.
// A UI confirmation is not an execution admission and cannot create that policy.
func Review(ctx context.Context, identity Identity) (string, error) {
	frozen, err := freeze(identity)
	if err != nil {
		return "", err
	}
	var admitted admission
	if err := exchange(ctx, map[string]any{"action": "validation-review", "identity": identity}, &admitted); err != nil {
		return "", err
	}
	if admitted.frozenIdentity != frozen || admitted.ReviewDigest != reviewDigest(frozen) || !digestPattern.MatchString(admitted.EnvironmentDigest) {
		return "", errors.New("trusted validation admission identity mismatch")
	}
	return admitted.ReviewDigest, nil
}

func decodeSample(encoded string) ([]byte, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxSample) {
		return nil, errors.New("sample exceeds validation byte budget")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxSample {
		return nil, errors.New("nonempty bounded base64 sample required")
	}
	return data, nil
}

// Evaluate uses a durable caller-owned key. Each distinct sample must have its own
// stable key; retries query the recorded execution and never blindly replay it.
func Evaluate(ctx context.Context, executionKey string, identity Identity, sample Sample) (Result, error) {
	result := Result{Status: "environment_error", Reason: "validation environment unavailable"}
	frozen, err := freeze(identity)
	if err != nil {
		return result, err
	}
	input, inputErr := decodeSample(sample.InputBase64)
	expected, expectedErr := decodeSample(sample.ExpectedOutputBase64)
	if inputErr != nil || expectedErr != nil {
		return Result{Status: "insufficient_evidence", Terminal: true, Reason: "authorized bounded input and exact expected CSV bytes required"}, nil
	}
	if len(executionKey) == 0 || len(executionKey) > 512 {
		return result, errors.New("stable bounded validation execution key required")
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	key := digest([]byte("aim-skill-validation-v1\x00" + executionKey))
	inputDigest, expectedDigest, reviewedDigest := digest(input), digest(expected), reviewDigest(frozen)
	matches := func(current *record) bool {
		return current != nil && current.ExecutionID != "" && current.RunID == key && current.InputSHA256 == inputDigest &&
			current.ImageID == frozen.ImageID && current.WorkerSHA256 == frozen.WorkerSHA256 && current.Validation.Identity == frozen &&
			current.Validation.ReviewDigest == reviewedDigest && current.Validation.ExpectedSHA256 == expectedDigest &&
			digestPattern.MatchString(current.Validation.EnvironmentDigest)
	}
	var current *record
	if err := exchange(ctx, map[string]any{"action": "validation-lookup", "execution_key": key}, &current); err != nil {
		return result, err // Unknown lookup is not authority to submit.
	}
	cleaned := false
	defer func() {
		if cleaned {
			return
		}
		// Cancellation/disconnection never implies stop. Resolve the exact journal
		// identity using an independent, finite safety context, then request stop.
		safety, done := context.WithTimeout(context.Background(), 12*time.Second)
		defer done()
		if current == nil {
			_ = exchange(safety, map[string]any{"action": "validation-lookup", "execution_key": key}, &current)
		}
		if matches(current) {
			var stopped record
			_ = exchange(safety, map[string]any{"action": "validation-stop", "execution_id": current.ExecutionID}, &stopped)
		}
	}()
	if current == nil {
		request := map[string]any{"execution_key": key, "identity": frozen, "input_b64": sample.InputBase64,
			"expected_sha256": expectedDigest}
		if err := exchange(ctx, map[string]any{"action": "validation-submit", "request": request}, &current); err != nil {
			// A submit error may mean a lost response or a concurrent winner. Query
			// once; never send another submit in this attempt.
			if lookupErr := exchange(ctx, map[string]any{"action": "validation-lookup", "execution_key": key}, &current); lookupErr != nil || current == nil {
				return result, errors.New("validation submission outcome unknown; retry the same execution key to reconcile")
			}
		}
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !matches(current) {
			return result, errors.New("validation key reused with different content or untrusted execution identity")
		}
		result.ExecutionID = current.ExecutionID
		result.EnvironmentDigest = current.Validation.EnvironmentDigest
		if current.Phase == "unknown" {
			result.Reason = "execution or cleanup is unconfirmed; reconcile the same execution key"
			return result, nil
		}
		if current.Phase == "cleaned" {
			cleaned = true
			if !current.Removed || !current.ResourcesAbsent || !current.StopConfirmed || current.Wait == nil || !digestPattern.MatchString(current.ContainerID) {
				result.Reason = "execution cleanup proof incomplete"
				return result, nil
			}
			result.Terminal = true
			if current.Outcome != "succeeded" {
				if current.FailureClass == "worker_failure" {
					result.Status = "failed"
					result.Reason = "reviewed CSV worker failed or exceeded its fixed execution budgets"
				} else {
					result.Reason = "validation runtime failed or stopped without a confirmed worker-quality result"
				}
				return result, nil
			}
			if *current.Wait != 0 {
				result.Reason = "validation runtime exit evidence is inconsistent"
				return result, nil
			}
			if len(current.Files) != 1 || current.Files[0].Name != "result.csv" {
				result.Reason = "fixed CSV artifact missing"
				return result, nil
			}
			actual, err := decodeSample(current.Files[0].DataBase64)
			if err != nil {
				result.Reason = "fixed CSV artifact encoding invalid"
				return result, nil
			}
			result.OutputDigest = digest(actual)
			result.Status = "failed"
			result.Reason = "CSV artifact bytes differ from expected output"
			if bytes.Equal(actual, expected) {
				result.Status = "passed"
				result.Reason = "exact CSV artifact bytes matched; fresh container and resource cleanup confirmed"
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			result.Reason = "validation deadline or cancellation; stop requested, cleanup not yet confirmed"
			return result, nil
		case <-ticker.C:
		}
		var next record
		if err := exchange(ctx, map[string]any{"action": "validation-get", "execution_id": current.ExecutionID}, &next); err != nil {
			return result, err
		}
		current = &next
	}
}

// boundedBuffer terminates transport capture rather than silently truncating it.
type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		return 0, errors.New("validation transport byte budget exceeded")
	}
	return b.buffer.Write(data)
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func exchange(ctx context.Context, request any, result any) error {
	payload, err := json.Marshal(request)
	if err != nil || len(payload)+1 > maxRequest {
		return errors.New("validation request exceeds finite protocol budget")
	}
	payload = append(payload, '\n')
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	socketPath := os.Getenv("AIM_SKILL_VALIDATION_SOCKET")
	target := os.Getenv("AIM_SKILL_VALIDATION_SSH_TARGET")
	if (socketPath == "") == (target == "") {
		return errors.New("configure exactly one trusted skill validation transport")
	}
	var response []byte
	if socketPath != "" {
		if !strings.HasPrefix(socketPath, "/") {
			return errors.New("validation socket must be an absolute node-local path")
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		if err != nil {
			return errors.New("trusted validation socket unavailable")
		}
		defer connection.Close()
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		defer stop()
		deadline, _ := ctx.Deadline()
		_ = connection.SetDeadline(deadline)
		if _, err := io.Copy(connection, bytes.NewReader(payload)); err != nil {
			return errors.New("validation request delivery unconfirmed")
		}
		response, err = io.ReadAll(io.LimitReader(connection, maxResponse+1))
		if err != nil {
			return errors.New("validation response incomplete")
		}
	} else {
		if !targetPattern.MatchString(target) {
			return errors.New("invalid configured restricted SSH target")
		}
		args := []string{"-T", "-oBatchMode=yes", "-oStrictHostKeyChecking=yes", "-oConnectTimeout=5", "-oConnectionAttempts=1",
			"-oClearAllForwardings=yes", "-oRequestTTY=no", "-oPermitLocalCommand=no", "-oRemoteCommand=none"}
		if identityFile := os.Getenv("AIM_SKILL_VALIDATION_SSH_IDENTITY_FILE"); identityFile != "" {
			if !strings.HasPrefix(identityFile, "/") {
				return errors.New("validation SSH identity path must be absolute")
			}
			args = append(args, "-oIdentitiesOnly=yes", "-i", identityFile)
		}
		args = append(args, "--", target)
		command := exec.CommandContext(ctx, "/usr/bin/ssh", args...)
		command.Stdin = bytes.NewReader(payload)
		stdout := &boundedBuffer{limit: maxResponse}
		stderr := &boundedBuffer{limit: 4096}
		command.Stdout, command.Stderr = stdout, stderr
		command.WaitDelay = time.Second
		runErr := command.Run()
		response = stdout.Bytes()
		// The forced bridge exits 1 for a complete manager rejection response.
		// Parse that response, but never expose SSH stderr or remote diagnostics.
		if runErr != nil && (len(response) == 0 || ctx.Err() != nil) {
			return errors.New("restricted validation SSH response unconfirmed")
		}
	}
	if len(response) == 0 || len(response) > maxResponse || response[len(response)-1] != '\n' {
		return errors.New("validation response exceeds budget or is incomplete")
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if json.Unmarshal(response, &envelope) != nil || !envelope.OK {
		return errors.New("trusted execution manager refused validation admission or command")
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return errors.New("invalid trusted validation response")
	}
	return nil
}
