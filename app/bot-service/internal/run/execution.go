package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maomeng/aim/app/bot-service/internal/component"
	"github.com/maomeng/aim/app/bot-service/internal/execution"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	"github.com/maomeng/aim/app/bot-service/internal/skillvalidation"
	filepb "github.com/maomeng/aim/app/file-service/pb/file"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	msg "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/protocol"
	"github.com/maomeng/aim/pkg/resourcegrant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxExecutionInput = 65536
	executionPollWait = 60 * time.Second
	executionPollStep = 200 * time.Millisecond
)

// executionTool is available only when this Run froze at least one published
// fixed-script capability. The model selects one exact version and one exact
// accepted file material; it cannot supply code, image, entry, command, limits
// or a host path.
func executionTool() component.RunTool {
	return component.RunTool{ServerID: "runtime", Name: "run_skill_script", Risk: "read", CanShare: true,
		Description: "Run one published, human-reviewed fixed script skill over exactly one currently accepted CSV file material and register its fixed result.csv as this Run's formal file result. Use only when the task asks to process that file. version_id must come from the frozen skill catalog and file_id from accepted material.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"version_id":{"type":"string","description":"Frozen skill version identity with a reviewed fixed script"},"file_id":{"type":"string","description":"Accepted CSV file material from this Run"}},"required":["version_id","file_id"],"additionalProperties":false}`)}
}

// executionFailure carries a safe, viewer-facing explanation while preserving
// a gRPC code for the existing Run failure mapping.
type executionFailure struct {
	code   codes.Code
	reason string
}

func (e *executionFailure) Error() string { return e.reason }
func (e *executionFailure) GRPCStatus() *status.Status {
	return status.New(e.code, e.reason)
}

type executionArguments struct {
	VersionID string `json:"version_id"`
	FileID    string `json:"file_id"`
}

func decodeExecutionArguments(raw string) (executionArguments, error) {
	var args executionArguments
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return args, status.Error(codes.InvalidArgument, "invalid fixed-script arguments")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return args, status.Error(codes.InvalidArgument, "trailing fixed-script arguments")
	}
	if identity.Validate(args.VersionID) != nil || identity.Validate(args.FileID) != nil {
		return args, status.Error(codes.InvalidArgument, "fixed-script requires exact version_id and file_id")
	}
	return args, nil
}

// performExecutionTool owns one durable fixed-workload identity per Run. Worker
// lease generations fence mutations, but never change the workload's identity.
// Replayed or repeated calls observe that identity and reuse its formal file.
func (s *Service) performExecutionTool(ctx context.Context, r *model.Run, generation int64, config ExecutionConfig, call *llm.ToolCall) error {
	current, err := s.executionBoundary(ctx, r, generation)
	if err != nil {
		return err
	}
	r = current
	args, err := decodeExecutionArguments(call.Function.Arguments)
	if err != nil {
		return err
	}
	frozen, ok := frozenSkill(config, args.VersionID)
	if !ok || !frozen.Executable {
		return status.Error(codes.PermissionDenied, "skill version has no frozen fixed-script capability")
	}
	if s.files == nil {
		return status.Error(codes.Unavailable, "file service unavailable")
	}
	contract, err := skill.LoadExecution(ctx, s.svc.DB.DB, r.BotID, frozen)
	if err != nil {
		return err
	}
	admission, err := skillvalidation.Review(ctx, contract.Identity)
	if err != nil || admission != contract.CodeReviewIdentity {
		return status.Error(codes.FailedPrecondition, "trusted execution admission changed; a new reviewed version is required")
	}
	content, err := s.executionInput(ctx, r, args.FileID)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(content)
	inputDigest := hex.EncodeToString(sum[:])
	limits := execution.DefaultLimits()

	record, created, err := s.prepareExecution(ctx, r, generation, frozen, args.FileID, inputDigest, int64(len(content)), contract)
	if err != nil {
		return err
	}
	if fileID := completedExecutionFile(record); fileID != "" {
		return s.persistExecutionToolResult(ctx, r, generation, call, fileID, record.OutputBytes, record.OutputSHA256, stringValue(record.ResultGrantID))
	}
	if record.Phase == "unknown" {
		return &executionFailure{code: codes.Unavailable, reason: "file execution outcome is unconfirmed; the same execution will not be replayed"}
	}
	if !created && record.ExecutionID == nil {
		// The intent exists but no manager execution was recorded. Reconcile by
		// stable Run/generation before deciding whether submission is safe.
		record, err = s.reconcileExecutionRecord(ctx, record)
		if err != nil {
			return err
		}
		if fileID := completedExecutionFile(record); fileID != "" {
			return s.persistExecutionToolResult(ctx, r, generation, call, fileID, record.OutputBytes, record.OutputSHA256, stringValue(record.ResultGrantID))
		}
	}
	if record.ExecutionID == nil {
		request := execution.SubmitRequest{RunID: r.ID, Generation: record.Generation, Capability: "csv-v1", InputBase64: base64.StdEncoding.EncodeToString(content), Limits: limits}
		managerRecord, submitErr := execution.SubmitRun(ctx, request)
		if submitErr != nil {
			// A refusal and a lost response are indistinguishable on the wire.
			// Query the stable identity once; never send a second submit.
			looked, lookupErr := execution.LookupRun(ctx, r.ID, record.Generation)
			if lookupErr != nil {
				s.markExecutionUnknown(ctx, record)
				return &executionFailure{code: codes.Unavailable, reason: "file execution submission outcome is unknown; no replay attempted"}
			}
			managerRecord = looked
		}
		record, err = s.saveManagerRecord(ctx, record, managerRecord)
		if err != nil {
			return err
		}
	}
	record, managerRecord, err := s.pollExecution(ctx, record)
	if err != nil {
		return err
	}
	return s.finishExecution(ctx, r, generation, call, record, managerRecord)
}

// executionInput re-reads the accepted material under current authority and
// requires exact equality with the accepted snapshot. Input is bounded data,
// never a command, source file or runtime dependency request.
func (s *Service) executionInput(ctx context.Context, r *model.Run, fileID string) ([]byte, error) {
	var materials []Material
	if err := json.Unmarshal(r.Materials, &materials); err != nil {
		return nil, status.Error(codes.FailedPrecondition, "accepted materials unavailable")
	}
	found := false
	for _, material := range materials {
		if material.Kind == "file" && material.FileID == fileID {
			found = true
			break
		}
	}
	if !found {
		return nil, status.Error(codes.PermissionDenied, "file is not current accepted material of this Run")
	}
	fresh, err := s.readFileMaterial(ctx, Input{BotID: r.BotID, ConvID: r.ConvID, BindingID: r.BindingID, RequesterID: r.RequesterID, Visibility: r.Visibility, ResourceGrantIDs: r.ResourceGrantIDs}, fileID)
	if err != nil {
		return nil, err
	}
	var accepted string
	for _, material := range materials {
		if material.Kind == "file" && material.FileID == fileID {
			accepted = material.Content
		}
	}
	if fresh.Content != accepted {
		return nil, status.Error(codes.FailedPrecondition, "accepted file material changed; start a new Run with current material")
	}
	content := []byte(accepted)
	if len(content) == 0 || len(content) > maxExecutionInput || !utf8.Valid(content) || bytes.ContainsRune(content, 0) {
		return nil, status.Error(codes.InvalidArgument, "file material must be bounded UTF-8 CSV data")
	}
	return content, nil
}

func (s *Service) prepareExecution(ctx context.Context, r *model.Run, generation int64, frozen skill.Frozen, fileID, inputDigest string, inputBytes int64, contract *skill.ExecutionContract) (*model.RunExecution, bool, error) {
	var record *model.RunExecution
	created := false
	_, err := s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "run-execution:"+r.ID).Error; err != nil {
			return err
		}
		var existing model.RunExecution
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ?", r.ID).Take(&existing).Error
		if err == nil {
			record = &existing
			return verifyExecutionIdentity(record, frozen, fileID, inputDigest, contract)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		now := time.Now()
		fresh := model.RunExecution{
			ID: id, RunID: r.ID, Generation: generation, InputVersion: current.AppliedInputVersion,
			SkillVersionID: frozen.VersionID, SkillDigest: frozen.Digest,
			InputFileID: fileID, InputSHA256: inputDigest, InputBytes: inputBytes,
			ImageID: contract.Identity.ImageDigest, WorkerSHA256: workerDigest(contract), EntryPoint: contract.Identity.EntryPoint,
			ReviewDigest: contract.CodeReviewIdentity, Phase: "intent", Outcome: "unknown",
			Evidence: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&fresh).Error; err != nil {
			return err
		}
		record, created = &fresh, true
		return nil
	})
	return record, created, err
}

func workerDigest(contract *skill.ExecutionContract) string {
	// The manager admits exactly one reviewed source. Keeping its digest avoids
	// persisting a second copy of the script in the execution row.
	if len(contract.Identity.Scripts) == 1 {
		if source, ok := contract.Identity.Scripts["worker.py"]; ok {
			sum := sha256.Sum256([]byte(source))
			return hex.EncodeToString(sum[:])
		}
	}
	return ""
}

func verifyExecutionIdentity(record *model.RunExecution, frozen skill.Frozen, fileID, inputDigest string, contract *skill.ExecutionContract) error {
	if record.SkillVersionID != frozen.VersionID || record.SkillDigest != frozen.Digest || record.InputFileID != fileID || record.InputSHA256 != inputDigest || record.ImageID != contract.Identity.ImageDigest || record.EntryPoint != contract.Identity.EntryPoint || record.ReviewDigest != contract.CodeReviewIdentity {
		return status.Error(codes.FailedPrecondition, "this Run generation already records a different fixed-file execution")
	}
	return nil
}

func completedExecutionFile(record *model.RunExecution) string {
	if record.Phase == "cleaned" && record.Outcome == "succeeded" && record.ResultFileID != nil {
		return *record.ResultFileID
	}
	return ""
}

func (s *Service) reconcileExecutionRecord(ctx context.Context, record *model.RunExecution) (*model.RunExecution, error) {
	managerRecord, err := execution.LookupRun(ctx, record.RunID, record.Generation)
	if err != nil {
		if _, rejected := err.(*execution.Rejected); rejected {
			// The manager has no record and refused a fresh execution? A refusal
			// here is authoritative only for an already-known intent; keep the
			// unconfirmed state rather than inventing "never created".
			s.markExecutionUnknown(ctx, record)
			return record, &executionFailure{code: codes.Unavailable, reason: "file execution identity could not be reconciled; no replay attempted"}
		}
		s.markExecutionUnknown(ctx, record)
		return record, &executionFailure{code: codes.Unavailable, reason: "file execution identity unconfirmed; no replay attempted"}
	}
	return s.saveManagerRecord(ctx, record, managerRecord)
}

func (s *Service) saveManagerRecord(ctx context.Context, record *model.RunExecution, manager execution.Record) (*model.RunExecution, error) {
	record.ExecutionID = stringPtr(manager.ExecutionID)
	record.ContainerID = manager.ContainerID
	record.Phase = manager.Phase
	record.Outcome = manager.Outcome
	record.FailureClass = manager.FailureClass
	record.Evidence = sanitizeEvidence(manager.Evidence)
	record.UpdatedAt = time.Now()
	if err := s.svc.DB.WithContext(ctx).Model(record).Select("execution_id", "container_id", "phase", "outcome", "failure_class", "evidence", "updated_at").Updates(record).Error; err != nil {
		return record, err
	}
	return record, nil
}

// sanitizeEvidence retains bounded execution facts needed for lifecycle and
// viewer projection. Host paths, cgroup snapshots, process tables, container
// addresses and raw stderr never enter the Bot domain.
func sanitizeEvidence(raw json.RawMessage) json.RawMessage {
	allowed := map[string]bool{"removed": true, "resources_absent": true, "stop_confirmed": true, "wait": true,
		"never_started": true, "input_delivered": true, "workload_timeout": true, "attached_exit_code": true,
		"attached_bytes": true, "output_bytes": true, "output_files": true, "no_create_attempted": true}
	var source map[string]any
	if json.Unmarshal(raw, &source) != nil {
		return json.RawMessage(`{}`)
	}
	result := make(map[string]any)
	for key, value := range source {
		if allowed[key] {
			result[key] = value
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func (s *Service) markExecutionUnknown(ctx context.Context, record *model.RunExecution) {
	record.Phase, record.Outcome, record.UpdatedAt = "unknown", "unknown", time.Now()
	_ = s.svc.DB.WithContext(ctx).Model(record).Select("phase", "outcome", "updated_at").Updates(record).Error
}

func (s *Service) pollExecution(ctx context.Context, record *model.RunExecution) (*model.RunExecution, *execution.Record, error) {
	if record.ExecutionID == nil {
		return record, nil, &executionFailure{code: codes.Unavailable, reason: "file execution identity unavailable"}
	}
	var last *execution.Record
	failures := 0
	deadline := time.Now().Add(executionPollWait)
	for {
		if record.Phase == "cleaned" {
			// The manager retains the bounded result with its durable record.
			// Recover it after a crash between cleanup and file registration.
			if last == nil {
				managerRecord, err := execution.GetRun(ctx, *record.ExecutionID)
				if err != nil {
					return record, nil, &executionFailure{code: codes.Unavailable, reason: "file execution result could not be recovered from trusted state"}
				}
				last = &managerRecord
			}
			return record, last, nil
		}
		if record.Phase == "unknown" {
			return record, last, &executionFailure{code: codes.Unavailable, reason: "file execution process or cleanup is unconfirmed"}
		}
		if ctx.Err() != nil {
			s.stopExecutionSafety(record)
			return record, last, ctx.Err()
		}
		if time.Now().After(deadline) {
			s.stopExecutionSafety(record)
			s.markExecutionUnknown(ctx, record)
			return record, last, &executionFailure{code: codes.DeadlineExceeded, reason: "file execution did not reach verified cleanup before the delivery deadline"}
		}
		select {
		case <-ctx.Done():
			s.stopExecutionSafety(record)
			return record, last, ctx.Err()
		case <-time.After(executionPollStep):
		}
		managerRecord, err := execution.GetRun(ctx, *record.ExecutionID)
		if err != nil {
			failures++
			if failures < 3 {
				continue
			}
			s.stopExecutionSafety(record)
			return record, last, &executionFailure{code: codes.Unavailable, reason: "trusted execution manager unavailable; execution state unconfirmed"}
		}
		failures = 0
		last = &managerRecord
		record, err = s.saveManagerRecord(ctx, record, managerRecord)
		if err != nil {
			return record, last, err
		}
	}
}

// stopExecutionSafety uses an independent finite context. Cancellation or a
// closed stream never by itself means the container stopped.
func (s *Service) stopExecutionSafety(record *model.RunExecution) {
	if record.ExecutionID == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	managerRecord, err := execution.StopRun(stopCtx, *record.ExecutionID)
	if err != nil {
		s.markExecutionUnknown(context.Background(), record)
		return
	}
	for attempt := 0; attempt < 40 && managerRecord.Phase != "cleaned" && managerRecord.Phase != "unknown"; attempt++ {
		select {
		case <-stopCtx.Done():
			s.markExecutionUnknown(context.Background(), record)
			return
		case <-time.After(executionPollStep):
		}
		managerRecord, err = execution.GetRun(stopCtx, *record.ExecutionID)
		if err != nil {
			s.markExecutionUnknown(context.Background(), record)
			return
		}
	}
	if _, err := s.saveManagerRecord(context.Background(), record, managerRecord); err != nil {
		s.markExecutionUnknown(context.Background(), record)
	}
}

func (s *Service) finishExecution(ctx context.Context, r *model.Run, generation int64, call *llm.ToolCall, record *model.RunExecution, manager *execution.Record) error {
	removed, resourcesAbsent, stopConfirmed, wait := recordCleanup(record)
	neverStarted, _ := executionStartEvidence(record)
	if record.Phase != "cleaned" || !removed || !resourcesAbsent || !stopConfirmed || (wait == nil && !neverStarted) || record.ContainerID == nil {
		s.markExecutionUnknown(ctx, record)
		return &executionFailure{code: codes.Unavailable, reason: "file execution cleanup proof is incomplete"}
	}
	if record.Outcome != "succeeded" {
		return executionOutcomeFailure(record)
	}
	if neverStarted || wait == nil || *wait != 0 {
		return &executionFailure{code: codes.Unavailable, reason: "file execution exit evidence is inconsistent"}
	}
	if manager == nil || manager.Result == nil || len(manager.Result.Files) != 1 || manager.Result.Files[0].Name != "result.csv" || manager.Result.Files[0].DataB64 == "" {
		return &executionFailure{code: codes.Unavailable, reason: "fixed CSV artifact is missing"}
	}
	data, err := base64.StdEncoding.Strict().DecodeString(manager.Result.Files[0].DataB64)
	if err != nil || len(data) == 0 || len(data) > maxExecutionInput || !utf8Text(data) {
		return &executionFailure{code: codes.ResourceExhausted, reason: "fixed CSV artifact bytes are invalid or exceed the delivery budget"}
	}
	sum := sha256.Sum256(data)
	outputDigest := hex.EncodeToString(sum[:])
	if file := manager.Result.Files[0]; file.SHA256 != "" && file.SHA256 != outputDigest || file.Size != 0 && file.Size != int64(len(data)) {
		return &executionFailure{code: codes.Unavailable, reason: "fixed CSV artifact summary is inconsistent"}
	}
	response, err := s.files.RegisterBotOutput(resourcegrant.Context(ctx, resourceAccess(r)), &filepb.RegisterBotOutputReq{
		RunId: r.ID, Generation: record.Generation, WorkerGeneration: generation, Name: "result.csv", MimeType: "text/csv", Content: data, Sha256: outputDigest,
	})
	if err != nil {
		return err
	}
	if response.GetFile() == nil || identity.Validate(response.GetFile().GetFileId()) != nil || response.GetFile().GetSize() != int64(len(data)) {
		return status.Error(codes.Unavailable, "file service returned an invalid output identity")
	}
	record.OutputSHA256, record.OutputBytes, record.ResultFileID, record.ResultGrantID, record.UpdatedAt = outputDigest, int64(len(data)), &response.File.FileId, &response.GrantId, time.Now()
	if err := s.svc.DB.WithContext(ctx).Model(record).Select("output_sha256", "output_bytes", "result_file_id", "result_grant_id", "updated_at").Updates(record).Error; err != nil {
		return err
	}
	return s.persistExecutionToolResult(ctx, r, generation, call, response.File.FileId, int64(len(data)), outputDigest, response.GrantId)
}

func executionStartEvidence(record *model.RunExecution) (neverStarted, noCreate bool) {
	var evidence struct {
		NeverStarted bool `json:"never_started"`
		NoCreate     bool `json:"no_create_attempted"`
	}
	if len(record.Evidence) > 0 {
		_ = json.Unmarshal(record.Evidence, &evidence)
	}
	return evidence.NeverStarted, evidence.NoCreate
}

func recordCleanup(record *model.RunExecution) (bool, bool, bool, *int) {
	var evidence struct {
		Removed         bool `json:"removed"`
		ResourcesAbsent bool `json:"resources_absent"`
		StopConfirmed   bool `json:"stop_confirmed"`
		Wait            *int `json:"wait"`
	}
	if len(record.Evidence) > 0 {
		_ = json.Unmarshal(record.Evidence, &evidence)
	}
	return evidence.Removed, evidence.ResourcesAbsent, evidence.StopConfirmed, evidence.Wait
}

func executionOutcomeFailure(record *model.RunExecution) error {
	switch record.Outcome {
	case "output_limit":
		return &executionFailure{code: codes.ResourceExhausted, reason: "file execution exceeded its fixed output or log budget"}
	case "timeout":
		return &executionFailure{code: codes.DeadlineExceeded, reason: "file execution timed out and the container was stopped"}
	case "stopped":
		return &executionFailure{code: codes.Unavailable, reason: "file execution was stopped before producing a verified result"}
	default:
		if record.FailureClass == "worker_failure" {
			return &executionFailure{code: codes.FailedPrecondition, reason: "fixed file processing script failed or exceeded its input budget"}
		}
		return &executionFailure{code: codes.Unavailable, reason: "file execution environment failed before a verified result"}
	}
}

// Bind the selected result and pair its tool response in one fenced transaction.
// A new explicit call may select an existing artifact; a response to an older
// input remains a historical execution fact, never the new input's final result.
func (s *Service) persistExecutionToolResult(ctx context.Context, r *model.Run, generation int64, call *llm.ToolCall, fileID string, size int64, digest, grantID string) error {
	if identity.Validate(grantID) != nil {
		return status.Error(codes.Unavailable, "registered output grant is missing")
	}
	content, err := protocol.Marshal(&msg.FileContent{FileId: fileID, Name: "result.csv", Size: size, Ext: ".csv", MimeType: "text/csv"})
	if err != nil {
		return err
	}
	_, err = s.repo.Change(ctx, r.ID, generation, func(tx *gorm.DB, current *model.Run) error {
		if !slices.Contains(current.ResourceGrantIDs, grantID) {
			current.ResourceGrantIDs = append(current.ResourceGrantIDs, grantID)
		}
		selected := current.InputVersion == r.AppliedInputVersion && current.AppliedInputVersion == r.AppliedInputVersion
		note := "Historical registered artifact; newer accepted input supersedes this selection. Do not claim it as the current final result."
		if selected {
			current.ResultType, current.ResultText, current.ResultContent = "file", "result.csv", content
			current.ResultInvalidated = false
			note = "The registered file is this Run's formal result. Do not claim any other path or URL."
		}
		raw, err := json.Marshal(map[string]any{"status": "succeeded", "selected": selected, "file_id": fileID, "name": "result.csv", "bytes": size, "sha256": digest, "message": note})
		if err != nil {
			return err
		}
		if err := repo.AppendRunBlock(tx, current, "message", &llm.Message{Role: "tool", Name: call.Function.Name, ToolCallId: call.Id, Content: string(raw)}); err != nil {
			return err
		}
		var sources []map[string]any
		if err := json.Unmarshal(current.Sources, &sources); err != nil {
			return err
		}
		if !slices.ContainsFunc(sources, func(source map[string]any) bool {
			return source["type"] == "tool" && source["tool_name"] == call.Function.Name
		}) {
			sources = append(sources, map[string]any{"type": "tool", "tool_name": call.Function.Name})
			current.Sources, err = json.Marshal(sources)
			if err != nil {
				return err
			}
		}
		return repo.AppendRunBlock(tx, current, "tool_roundtrip", map[string]any{"tool_call": call, "result": string(raw), "failed": false, "executed": true})
	})
	return err
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func utf8Text(data []byte) bool {
	return utf8.Valid(data) && !bytes.ContainsRune(data, 0)
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// executionFailureReason turns recorded execution facts into a bounded Run
// error. It never exposes host paths, stdout/stderr, container addresses or raw
// execution output.
func (s *Service) executionFailureReason(ctx context.Context, runID string) string {
	var record model.RunExecution
	if err := s.svc.DB.WithContext(ctx).Where("run_id = ?", runID).Take(&record).Error; err != nil {
		return ""
	}
	switch record.Phase {
	case "unknown":
		return "file execution process or cleanup is unconfirmed; external side effects remain unknown"
	case "cleaned":
	default:
		return "file execution did not reach verified cleanup"
	}
	switch record.Outcome {
	case "output_limit":
		return "file execution exceeded its fixed output or log budget"
	case "timeout":
		return "file execution timed out and the container was stopped"
	case "stopped":
		return "file execution was stopped before producing a verified result"
	case "failed":
		if record.FailureClass == "worker_failure" {
			return "fixed file processing script failed"
		}
		return "file execution environment failed before a verified result"
	}
	return ""
}

// latestExecution loads the public projection for a Run detail/list response.
func (s *Service) attachExecution(ctx context.Context, public *model.PublicRun) error {
	if public == nil {
		return nil
	}
	var record model.RunExecution
	err := s.svc.DB.WithContext(ctx).Where("run_id = ?", public.ID).Order("updated_at DESC").Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	public.Execution = record.Public()
	return nil
}
