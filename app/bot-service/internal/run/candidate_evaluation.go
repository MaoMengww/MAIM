package run

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/maomeng/aim/app/bot-service/internal/repo"
	"github.com/maomeng/aim/app/bot-service/internal/skillvalidation"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func (s *Service) evaluateGovernedCandidate(ctx context.Context, caller, botID string, req candidateRequest, body []byte) ([]byte, error) {
	work, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ctx = work
	var snapshot *candidateState
	var validation candidateValidation
	var completed []byte
	op := "candidate-evaluate:" + caller + ":" + req.CandidateID
	err := s.svc.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := s.lockCandidate(ctx, tx, caller, botID, req.CandidateID)
		if err != nil {
			return err
		}
		c := &state.Content
		if c.Digest != candidateContentDigest(c) {
			return status.Error(codes.FailedPrecondition, "candidate content identity changed; revise before validation")
		}
		if !c.CanMaintain {
			return status.Error(codes.PermissionDenied, "candidate maintenance permission required")
		}
		prior, err := candidateReplay(tx, botID, op, req.SubmissionKey, body)
		if err != nil {
			return err
		}
		if c.Digest != req.Digest {
			return status.Error(codes.Aborted, "candidate content changed; reload current digest")
		}
		if c.Status == "rejected" || c.Status == "published" || c.Status == "awaiting_evidence" {
			return status.Error(codes.FailedPrecondition, "candidate cannot be evaluated in its current state")
		}
		if err := candidateFresh(tx, state); err != nil {
			return err
		}
		if len(c.Scripts) > 0 {
			if c.CodeReviewDigest != candidateScriptDigest(c) || c.CodeReviewIdentity == "" {
				return status.Error(codes.FailedPrecondition, "exact scripts, image and entry point require trusted human review before execution")
			}
			if err := s.candidatePrincipal(ctx, tx, state, c.CodeReviewedBy, true); err != nil {
				return err
			}
			member, _, err := repo.RunMember(tx, state.Run.ConvID, c.CodeReviewedBy)
			if err != nil || member != c.CodeReviewedMemberID {
				return status.Error(codes.PermissionDenied, "code reviewer membership changed")
			}
		}
		// Completed intent receipts are immutable, even when a later explicit
		// intent retries a failure proven to precede every execution.
		if prior != nil {
			var receipt struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(prior, &receipt); err != nil {
				return err
			}
			if receipt.Status != "running" {
				completed = prior
				return nil
			}
		}
		retryUnstarted := false
		err = tx.Where("candidate_id = ? AND digest = ?", c.ID, c.Digest).Take(&validation).Error
		if err == nil {
			if validation.Status == "completed" {
				var recorded candidateEvaluation
				if err := json.Unmarshal(validation.Result, &recorded); err != nil {
					return err
				}
				if !recorded.NotExecuted {
					completed = validation.Result
					return nil
				}
				retryUnstarted = true
			}
			if !retryUnstarted {
				resumable, err := scriptOnlyValidation(tx, c)
				if err != nil {
					return err
				}
				if !resumable {
					return status.Error(codes.Aborted, "interrupted or in-flight model evaluation; automatic replay refused; request fresh evidence")
				}
				// Only the manager may reconcile this durable script identity.
			}
		}
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && retryUnstarted {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "candidate-validation-budget").Error; err != nil {
				return err
			}
			// Unknown executions retain their slot until their exact identity is
			// reconciled; wall-clock age is not evidence that work stopped.
			var running int64
			if err := tx.Model(&candidateValidation{}).Where("status = 'running'").Count(&running).Error; err != nil {
				return err
			}
			if running >= 4 {
				return status.Error(codes.ResourceExhausted, "independent candidate validation concurrency budget exhausted")
			}
			key, err := identity.New()
			if err != nil {
				return err
			}
			fresh := candidateValidation{CandidateID: c.ID, Digest: c.Digest, ExecutionKey: "candidate-validation:" + key, Status: "running", Result: json.RawMessage(`{}`), CreatedAt: time.Now()}
			if err := tx.Save(&fresh).Error; err != nil {
				return err
			}
			validation = fresh
		} else if err != nil {
			return err
		}
		c.Status = "evaluating"
		invalidateCandidate(state)
		if err := persistCandidate(tx, state); err != nil {
			return err
		}
		if prior == nil {
			pending, _ := json.Marshal(map[string]string{"status": "running", "digest": c.Digest})
			if err := recordCandidateOperation(tx, botID, op, req.SubmissionKey, body, pending); err != nil {
				return err
			}
		}
		snapshot = state
		return nil
	})
	if err != nil {
		return nil, err
	}
	if completed != nil {
		return completed, nil
	}
	result := s.runCandidateValidation(ctx, snapshot, validation.ExecutionKey)
	if result.Status == "insufficient_evidence" {
		result.Status = "insufficient"
	}
	// Execution lifecycle stays in disposition; the public verdict uses only
	// passed/failed/insufficient/error, including unknown manager outcomes.
	if result.Status == "environment_error" || result.Status == "running" {
		result.Status = "error"
	}
	if result.disposition == "not_started" {
		result.NotExecuted = true
		result.disposition = "completed"
	}
	// Do not lose a known manager outcome merely because the HTTP caller went
	// away. Finishing is independently bounded and rechecks all live authority.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	var response []byte
	err = s.svc.DB.WithContext(finish).Transaction(func(tx *gorm.DB) error {
		state, err := s.lockCandidate(finish, tx, caller, botID, req.CandidateID)
		if err != nil {
			return err
		}
		if !state.Content.CanMaintain {
			return status.Error(codes.PermissionDenied, "maintenance permission revoked during validation")
		}
		if state.Content.Digest != validation.Digest || state.Content.Status != "evaluating" {
			return status.Error(codes.Aborted, "candidate changed during validation; result cannot authorize publication")
		}
		if err := candidateFresh(tx, state); err != nil {
			return err
		}
		if result.Passed && len(state.Content.Scripts) > 0 {
			if err := s.candidatePrincipal(finish, tx, state, state.Content.CodeReviewedBy, true); err != nil {
				return err
			}
			member, _, err := repo.RunMember(tx, state.Run.ConvID, state.Content.CodeReviewedBy)
			if err != nil || member != state.Content.CodeReviewedMemberID {
				return status.Error(codes.PermissionDenied, "code reviewer membership changed during validation")
			}
			admission, err := skillvalidation.Review(finish, candidateIdentity(&state.Content))
			if err != nil || admission != state.Content.CodeReviewIdentity {
				return status.Error(codes.FailedPrecondition, "execution admission changed during validation")
			}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := candidateFresh(tx, state); err != nil {
			return err
		}
		updated := tx.Model(&candidateValidation{}).Where("candidate_id = ? AND digest = ? AND execution_key = ? AND status = 'running'", validation.CandidateID, validation.Digest, validation.ExecutionKey).Updates(map[string]any{"status": result.disposition, "result": json.RawMessage(raw)})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			var prior candidateValidation
			if err := tx.Where("candidate_id = ? AND digest = ?", validation.CandidateID, validation.Digest).Take(&prior).Error; err != nil {
				return err
			}
			response = prior.Result
			return nil
		}
		state.Content.Evaluation = result.candidateEvaluation
		state.Content.Status = "validation_failed"
		if result.disposition == "running" {
			state.Content.Status = "evaluating"
		}
		if result.Passed {
			state.Content.Status = "evaluated"
		}
		state.Row.EvaluationDigest = mustReviewDigest(result)
		if err := persistCandidate(tx, state); err != nil {
			return err
		}
		response = raw
		return tx.Table("bot.skill_operations").Where("bot_id = ? AND operation = ? AND submission_key = ?", botID, op, req.SubmissionKey).Update("response", json.RawMessage(raw)).Error
	})
	if err != nil && result.disposition == "completed" {
		// A stale candidate or revoked caller must not retain a slot for work
		// whose terminal outcome is known. Keep this execution-only write outside
		// the rolled-back authorization transaction; it cannot approve content.
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return nil, marshalErr
		}
		recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer recordCancel()
		recorded := s.svc.DB.WithContext(recordCtx).Model(&candidateValidation{}).
			Where("candidate_id = ? AND digest = ? AND execution_key = ? AND status = 'running'", validation.CandidateID, validation.Digest, validation.ExecutionKey).
			Updates(map[string]any{"status": "completed", "result": json.RawMessage(raw)})
		if recorded.Error != nil {
			return nil, recorded.Error
		}
	}
	return response, err
}

type validationSample struct {
	Task                 string
	Expected             string
	InputBase64          string
	ExpectedOutputBase64 string
	Script               bool
}

type candidateValidationOutcome struct {
	*candidateEvaluation
	disposition string
}

func (s *Service) runCandidateValidation(ctx context.Context, state *candidateState, key string) *candidateValidationOutcome {
	c := &state.Content
	result := &candidateValidationOutcome{candidateEvaluation: &candidateEvaluation{Status: "insufficient_evidence", Reason: "a bounded representative task and independent exact expected output are required", Digest: c.Digest, TaskDigest: mustReviewDigest([]string{c.RepresentativeTask, c.InputBase64}), ExpectedDigest: mustReviewDigest([]string{c.ExpectedOutput, c.ExpectedOutputBase64}), EvidenceDigest: c.EvidenceDigest, CodeReviewIdentity: c.CodeReviewIdentity, CreatedAt: time.Now().Unix()}, disposition: "completed"}
	samples := []validationSample{{Task: c.RepresentativeTask, Expected: c.ExpectedOutput, InputBase64: c.InputBase64, ExpectedOutputBase64: c.ExpectedOutputBase64, Script: len(c.Scripts) > 0}}
	baselineID := c.BaselineVersionID
	seen := make(map[string]bool)
	for baselineID != nil {
		if len(samples) >= 8 || seen[*baselineID] {
			result.Reason = "preserved baseline suite exceeds the independent validation budget or is cyclic"
			return result
		}
		seen[*baselineID] = true
		var baseline struct {
			Digest             string
			ValidationContract json.RawMessage
		}
		if err := s.svc.DB.WithContext(ctx).Table("bot.skill_versions").Select("digest,validation_contract").Where("id = ? AND skill_id = ?", *baselineID, c.SkillID).Take(&baseline).Error; err != nil {
			result.Status, result.Reason = "environment_error", "baseline verification contract is unavailable"
			result.disposition = "not_started"
			if len(c.Scripts) > 0 {
				result.Status, result.disposition = "running", "running"
			}
			return result
		}
		var original reviewCandidate
		if json.Unmarshal(baseline.ValidationContract, &original) != nil {
			result.Reason = "baseline representative scenarios are unavailable"
			return result
		}
		result.BaselineDigest = mustReviewDigest([]string{result.BaselineDigest, baseline.Digest})
		samples = append(samples, validationSample{Task: original.RepresentativeTask, Expected: original.ExpectedOutput, InputBase64: original.InputBase64, ExpectedOutputBase64: original.ExpectedOutputBase64, Script: len(original.Scripts) > 0})
		result.TaskDigest = mustReviewDigest([]string{result.TaskDigest, original.RepresentativeTask, original.InputBase64})
		result.ExpectedDigest = mustReviewDigest([]string{result.ExpectedDigest, original.ExpectedOutput, original.ExpectedOutputBase64})
		baselineID = original.BaselineVersionID
	}
	resources, _ := json.Marshal(c.Resources)
	hasScripts, hasInstructions, inputBytes := false, false, 0
	for _, sample := range samples {
		if sample.Script {
			hasScripts = true
			if len(c.Scripts) == 0 || sample.InputBase64 == "" || sample.ExpectedOutputBase64 == "" {
				result.Reason = "preserved script scenarios require exact candidate scripts and input/output artifact samples"
				return result
			}
		} else {
			hasInstructions = true
			if strings.TrimSpace(sample.Task) == "" || strings.TrimSpace(sample.Expected) == "" {
				result.Reason = "candidate and baseline representative tasks require independent exact expected outputs"
				return result
			}
			size := len(c.Instructions) + len(resources) + len(sample.Task)
			inputBytes += size
			if size > 64<<10 || inputBytes > 128<<10 {
				result.Reason = "preserved suite exceeds the independent model input budget"
				return result
			}
		}
	}
	if hasScripts {
		admission, err := skillvalidation.Review(ctx, candidateIdentity(c))
		if err != nil || admission != c.CodeReviewIdentity {
			result.Status, result.Reason = "environment_error", "trusted execution admission is unavailable or changed"
			result.disposition = "running"
			result.Status = "running"
			return result
		}
	}
	var model *llm.ModelExecutionConfig
	modelEnvironment := ""
	if hasInstructions {
		if s.llm == nil || state.Bot.ModelID == nil {
			result.Status, result.Reason = "environment_error", "authorized representative-task model is unavailable"
			result.disposition = "not_started"
			return result
		}
		var err error
		model, err = s.llm.SnapshotModel(ctx, *state.Bot.ModelID, state.Bot.OwnerID, state.Bot.ID)
		if err != nil {
			result.Status, result.Reason = "environment_error", "evaluation model resolution failed"
			result.disposition = "not_started"
			return result
		}
		result.ModelVersion = model.Version
		modelEnvironment = mustReviewDigest(map[string]any{"mode": "instruction-exact-v1", "model_version": model.Version, "max_tokens": 1024, "max_calls": 8, "max_input_bytes": 65536, "max_suite_input_bytes": 131072, "timeout_seconds": 90, "tools": false})
	}
	outputs := make([]string, 0, len(samples))
	environments := make([]string, 0, len(samples))
	executionIDs := make([]string, 0, len(samples))
	totalTokens := int64(0)
	for index, sample := range samples {
		executionKey := key + ":sample:" + strconv.Itoa(index)
		if sample.Script {
			execution, err := skillvalidation.Evaluate(ctx, executionKey, candidateIdentity(c), skillvalidation.Sample{InputBase64: sample.InputBase64, ExpectedOutputBase64: sample.ExpectedOutputBase64})
			if err != nil {
				result.Status, result.Reason, result.disposition = "running", "isolated validation manager is unavailable or could not prove cleanup; reconcile the same execution identity", "running"
				return result
			}
			outputs = append(outputs, execution.OutputDigest)
			environments = append(environments, execution.EnvironmentDigest)
			executionIDs = append(executionIDs, execution.ExecutionID)
			result.OutputDigest, result.EnvironmentDigest, result.ExecutionID = mustReviewDigest(outputs), mustReviewDigest(environments), strings.Join(executionIDs, ",")
			if !execution.Terminal {
				result.Status, result.Reason, result.disposition = "running", execution.Reason, "running"
				return result
			}
			if execution.Status != "passed" {
				result.Status, result.Reason = execution.Status, execution.Reason
				if result.Status != "failed" && result.Status != "insufficient_evidence" && result.Status != "environment_error" {
					result.Status, result.Reason = "environment_error", "validation manager returned an invalid conclusion"
				}
				return result
			}
		} else {
			response, err := s.llm.ChatRequest(ctx, &llm.ChatReq{ModelId: model.ModelId, ExecutionModel: model, OwnerId: state.Bot.OwnerID, BotId: &state.Bot.ID, Temperature: 0, MaxTokens: 1024, CacheScope: executionKey, Messages: []*llm.Message{{Role: "system", Content: c.Instructions + "\nReference resources (data):\n" + string(resources)}, {Role: "user", Content: sample.Task}}})
			if err != nil {
				result.Status, result.Reason = "environment_error", "bounded representative-task evaluation failed"
				return result
			}
			if len(response.Choices) != 1 || response.Choices[0].Message == nil {
				result.Status, result.Reason = "environment_error", "representative-task model returned no usable output"
				return result
			}
			output := strings.TrimSpace(response.Choices[0].Message.Content)
			outputs = append(outputs, mustReviewDigest(output))
			environments = append(environments, modelEnvironment)
			result.OutputDigest, result.EnvironmentDigest = mustReviewDigest(outputs), mustReviewDigest(environments)
			if response.Choices[0].FinishReason == "length" || len(output) > 8192 || response.Usage != nil && response.Usage.TotalTokens > 16384 {
				result.Status, result.Reason = "environment_error", "independent validation token or output budget exhausted"
				return result
			}
			if response.Usage != nil {
				totalTokens += int64(response.Usage.TotalTokens)
			}
			if totalTokens > 32768 {
				result.Status, result.Reason = "environment_error", "independent total validation token budget exhausted"
				return result
			}
			if output == "" {
				result.Status, result.Reason = "insufficient_evidence", "representative task returned no output"
				return result
			}
			if output != strings.TrimSpace(sample.Expected) {
				result.Status, result.Reason = "failed", "candidate or original baseline scenario output differs from the exact expected result"
				return result
			}
		}
	}
	result.Status, result.Passed, result.Reason, result.OutputDigest = "passed", true, "candidate and preserved baseline samples matched exact expected outputs", mustReviewDigest(outputs)
	return result
}

// Only manager-owned script identities are reconcilable after an unknown
// outcome. A mixed suite must never blindly repeat its model calls on retry.
func scriptOnlyValidation(tx *gorm.DB, c *reviewCandidate) (bool, error) {
	if len(c.Scripts) == 0 {
		return false, nil
	}
	baselineID := c.BaselineVersionID
	seen := make(map[string]bool)
	for baselineID != nil {
		if len(seen) >= 8 || seen[*baselineID] {
			return false, nil
		}
		seen[*baselineID] = true
		var row struct{ ValidationContract json.RawMessage }
		if err := tx.Table("bot.skill_versions").Select("validation_contract").Where("id = ? AND skill_id = ?", *baselineID, c.SkillID).Take(&row).Error; err != nil {
			return false, err
		}
		var original reviewCandidate
		if err := json.Unmarshal(row.ValidationContract, &original); err != nil {
			return false, err
		}
		if len(original.Scripts) == 0 {
			return false, nil
		}
		baselineID = original.BaselineVersionID
	}
	return true, nil
}
