package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maomeng/aim/app/bot-service/internal/client"
	llm "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/identity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// Evaluation is an explicit representative task, not a success heuristic.
	maxTaskBytes      = 16 << 10
	maxExpectedBytes  = 2 << 10
	maxObservedBytes  = 512
	evaluationTimeout = 90 * time.Second
)

type Service struct {
	db  *gorm.DB
	llm *client.LlmGatewayClient
}

func NewService(db *gorm.DB, llmClient *client.LlmGatewayClient) *Service {
	return &Service{db: db, llm: llmClient}
}

type botRow struct {
	ID                   string  `gorm:"column:id"`
	OwnerType            string  `gorm:"column:owner_type"`
	OwnerID              *string `gorm:"column:owner_id"`
	Status               string  `gorm:"column:status"`
	ModelID              *string `gorm:"column:model_id"`
	AuthorizationVersion int64   `gorm:"column:authorization_version"`
}

func (s *Service) owner(ctx context.Context, botID, caller string) (*botRow, error) {
	if identity.Validate(botID) != nil || identity.Validate(caller) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid Bot or caller identity")
	}
	var bot botRow
	if err := s.db.WithContext(ctx).Table("bots").Where("id = ?", botID).Take(&bot).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "Bot not found")
		}
		return nil, err
	}
	if bot.OwnerType != "user" || bot.OwnerID == nil || *bot.OwnerID != caller {
		return nil, status.Error(codes.PermissionDenied, "Bot owner permission required")
	}
	return &bot, nil
}

// Operation dispatches the management API. Every write requires a stable
// submission identity; response bytes are protocol JSON owned by the gateway.
func (s *Service) Operation(ctx context.Context, caller, botID, operation string, body []byte) ([]byte, error) {
	if len(body) > 1<<20 {
		return nil, status.Error(codes.InvalidArgument, "skill operation body exceeds 1 MiB")
	}
	// Derived candidates require source-material authorization, not just Bot ownership.
	if operation == "get" || operation == "evaluate" || operation == "approve" || operation == "publish" {
		var request struct {
			CandidateID string `json:"candidate_id"`
		}
		if json.Unmarshal(body, &request) == nil && identity.Validate(request.CandidateID) == nil {
			var count int64
			if err := s.db.WithContext(ctx).Table("bot.skill_candidates").Where("id = ? AND bot_id = ? AND source_review_id IS NOT NULL", request.CandidateID, botID).Count(&count).Error; err != nil {
				return nil, err
			}
			if count != 0 {
				return nil, status.Error(codes.PermissionDenied, "Run-derived candidate requires source-scoped candidate governance")
			}
		}
	}
	if operation == "available" {
		if identity.Validate(caller) != nil {
			return nil, status.Error(codes.Unauthenticated, "authenticated requester required")
		}
		available, err := s.available(ctx, botID)
		if err != nil {
			return nil, err
		}
		return marshal(available)
	}
	bot, err := s.owner(ctx, botID, caller)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "import":
		return s.importPackage(ctx, bot, body)
	case "list":
		list, err := s.list(ctx, bot)
		if err != nil {
			return nil, err
		}
		return marshal(list)
	case "get":
		return s.getCandidate(ctx, bot, body)
	case "evaluate":
		return s.evaluate(ctx, bot, body)
	case "approve":
		return s.approve(ctx, bot, body)
	case "publish":
		return s.publish(ctx, bot, body)
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown skill operation")
	}
}

// Available lists the enabled, published catalog for Run selection. It reveals
// no candidate content, evidence, or publisher identity.
func (s *Service) Available(ctx context.Context, botID string) ([]AvailableSkill, error) {
	if identity.Validate(botID) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid Bot identity")
	}
	return s.available(ctx, botID)
}

func (s *Service) available(ctx context.Context, botID string) ([]AvailableSkill, error) {
	var count int64
	if err := s.db.WithContext(ctx).Table("bots").Where("id = ?", botID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, status.Error(codes.NotFound, "Bot not found")
	}
	rows, err := s.enabledVersions(ctx, botID)
	if err != nil {
		return nil, err
	}
	result := make([]AvailableSkill, 0, len(rows))
	for _, row := range rows {
		result = append(result, AvailableSkill{VersionID: row.ID, Name: row.Name, Description: row.Description, Version: row.Version})
	}
	return result, nil
}

func (s *Service) importPackage(ctx context.Context, bot *botRow, body []byte) ([]byte, error) {
	var request importRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid import body")
	}
	if len(request.SubmissionKey) == 0 || len(request.SubmissionKey) > 256 {
		return nil, status.Error(codes.InvalidArgument, "submission_key is required")
	}
	if strings.TrimSpace(request.RepresentativeTask) == "" || len(request.RepresentativeTask) > maxTaskBytes {
		return nil, status.Error(codes.InvalidArgument, "representative_task must contain between 1 and 16384 bytes")
	}
	if strings.TrimSpace(request.ExpectedOutput) == "" || len(request.ExpectedOutput) > maxExpectedBytes {
		return nil, status.Error(codes.InvalidArgument, "expected_output must contain between 1 and 2048 bytes")
	}
	pkg, err := ParseArchive(request.Archive)
	if err != nil {
		var diagnostic *ImportError
		if errors.As(err, &diagnostic) {
			return nil, status.Error(codes.InvalidArgument, diagnostic.Reason)
		}
		return nil, err
	}
	return s.mutate(ctx, bot.ID, *bot.OwnerID, "import", request.SubmissionKey, body, func(tx *gorm.DB) (any, error) {
		// Serialize package identity creation across concurrent imports.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-package:"+bot.ID+":"+*bot.OwnerID+":"+pkg.Name).Error; err != nil {
			return nil, err
		}
		var pkgRow packageRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("bot_id = ? AND publisher_id = ? AND name = ? AND scope_key = 'bot'", bot.ID, *bot.OwnerID, pkg.Name).Take(&pkgRow).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			id, err := identity.New()
			if err != nil {
				return nil, err
			}
			pkgRow = packageRow{ID: id, BotID: bot.ID, PublisherID: *bot.OwnerID, Name: pkg.Name, License: pkg.License, CreatedAt: time.Now()}
			if err := tx.Create(&pkgRow).Error; err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		var baseline *string
		var latest version
		if err := tx.Where("skill_id = ? AND status = 'published' AND scope_key = 'bot'", pkgRow.ID).Order("version DESC").Take(&latest).Error; err == nil {
			baseline = &latest.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		var existing candidate
		err = tx.Where("skill_id = ? AND digest = ? AND COALESCE(baseline_version_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(?, '00000000-0000-0000-0000-000000000000'::uuid)", pkgRow.ID, pkg.Digest, baseline).Take(&existing).Error
		if err == nil {
			return viewCandidate(&existing, nil)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		resources, err := json.Marshal(pkg.Resources)
		if err != nil {
			return nil, err
		}
		id, err := identity.New()
		if err != nil {
			return nil, err
		}
		now := time.Now()
		created := candidate{ID: id, SkillID: pkgRow.ID, BotID: bot.ID, Digest: pkg.Digest, Name: pkg.Name, Description: pkg.Description, Instructions: pkg.Instructions, Resources: resources, License: pkg.License, BaselineVersionID: baseline, Status: "pending_validation", Task: request.RepresentativeTask, ExpectedOutput: request.ExpectedOutput, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&created).Error; err != nil {
			return nil, err
		}
		return viewCandidate(&created, nil)
	})
}

func (s *Service) list(ctx context.Context, bot *botRow) ([]PackageView, error) {
	var packages []packageRow
	if err := s.db.WithContext(ctx).Where("bot_id = ? AND scope_key = 'bot'", bot.ID).Order("name ASC").Find(&packages).Error; err != nil {
		return nil, err
	}
	result := make([]PackageView, 0, len(packages))
	for _, pkg := range packages {
		view := PackageView{SkillID: pkg.ID, Name: pkg.Name, License: pkg.License, PublisherID: pkg.PublisherID, Versions: []VersionView{}, Candidates: []CandidateView{}}
		var versions []version
		if err := s.db.WithContext(ctx).Where("skill_id = ? AND scope_key = 'bot'", pkg.ID).Order("version ASC").Find(&versions).Error; err != nil {
			return nil, err
		}
		var current binding
		hasBinding := true
		if err := s.db.WithContext(ctx).Where("bot_id = ? AND skill_id = ? AND scope_key = 'bot'", bot.ID, pkg.ID).Take(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			hasBinding = false
		} else if err != nil {
			return nil, err
		}
		for index := range versions {
			enabled := hasBinding && current.Enabled && current.VersionID == versions[index].ID
			view.Versions = append(view.Versions, *versionView(&versions[index], enabled))
			if enabled {
				selected := view.Versions[len(view.Versions)-1]
				view.EnabledVersion = &selected
			}
		}
		var candidates []candidate
		if err := s.db.WithContext(ctx).Where("skill_id = ? AND source_review_id IS NULL", pkg.ID).Order("created_at DESC").Find(&candidates).Error; err != nil {
			return nil, err
		}
		for index := range candidates {
			evaluation, err := s.latestEvaluation(ctx, &candidates[index])
			if err != nil {
				return nil, err
			}
			viewCandidate, err := viewCandidate(&candidates[index], evaluation)
			if err != nil {
				return nil, err
			}
			view.Candidates = append(view.Candidates, *viewCandidate)
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *Service) getCandidate(ctx context.Context, bot *botRow, body []byte) ([]byte, error) {
	var request struct {
		CandidateID string `json:"candidate_id"`
	}
	if err := json.Unmarshal(body, &request); err != nil || identity.Validate(request.CandidateID) != nil {
		return nil, status.Error(codes.InvalidArgument, "valid candidate_id is required")
	}
	var row candidate
	if err := s.db.WithContext(ctx).Where("id = ? AND bot_id = ?", request.CandidateID, bot.ID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "skill candidate not found")
		}
		return nil, err
	}
	evaluation, err := s.latestEvaluation(ctx, &row)
	if err != nil {
		return nil, err
	}
	view, err := viewCandidate(&row, evaluation)
	if err != nil {
		return nil, err
	}
	return marshal(view)
}

func (s *Service) latestEvaluation(ctx context.Context, row *candidate) (*evaluation, error) {
	if row.EvaluationDigest == "" {
		return nil, nil
	}
	var result evaluation
	if err := s.db.WithContext(ctx).Where("candidate_id = ? AND digest = ?", row.ID, row.EvaluationDigest).Take(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &result, nil
}

func viewCandidate(row *candidate, evaluation *evaluation) (*CandidateView, error) {
	view := &CandidateView{ID: row.ID, SkillID: row.SkillID, Name: row.Name, Description: row.Description, Digest: row.Digest, License: row.License, Status: row.Status, Failure: row.Failure, BaselineVersionID: row.BaselineVersionID, PublishedVersionID: row.PublishedVersionID, ApprovedDigest: row.ApprovedDigest, CreatedAt: row.CreatedAt.Unix()}
	if evaluation != nil {
		view.Evaluation = &EvaluationView{Status: evaluation.Status, Passed: evaluation.Status == "passed", Reason: evaluation.Reason, Digest: evaluation.Digest, TaskDigest: evaluation.TaskDigest, ExpectedDigest: evaluation.ExpectedHash, OutputDigest: evaluation.OutputDigest, ModelVersion: evaluation.ModelVersion, CreatedAt: evaluation.CreatedAt.Unix()}
	}
	return view, nil
}

func (s *Service) evaluate(ctx context.Context, bot *botRow, body []byte) ([]byte, error) {
	var request struct {
		CandidateID   string `json:"candidate_id"`
		SubmissionKey string `json:"submission_key"`
		Digest        string `json:"digest"`
	}
	if json.Unmarshal(body, &request) != nil || identity.Validate(request.CandidateID) != nil || request.SubmissionKey == "" || len(request.SubmissionKey) > 256 || request.Digest == "" {
		return nil, status.Error(codes.InvalidArgument, "candidate_id, submission_key and digest required")
	}
	if s.llm == nil || bot.ModelID == nil || *bot.ModelID == "" {
		return nil, status.Error(codes.FailedPrecondition, "Bot model is unavailable for independent evaluation")
	}
	var row candidate
	claimed := false
	prior, err := s.mutate(ctx, bot.ID, *bot.OwnerID, "evaluate", request.SubmissionKey, body, func(tx *gorm.DB) (any, error) {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND bot_id = ?", request.CandidateID, bot.ID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.NotFound, "skill candidate not found")
			}
			return nil, err
		}
		if row.Digest != request.Digest {
			return nil, status.Error(codes.Aborted, "candidate content changed")
		}
		if _, err := legacyCandidateAuthority(tx, &row, *bot.OwnerID); err != nil {
			return nil, err
		}
		if row.Status != "published" {
			if err := legacyBaseline(tx, &row); err != nil {
				return nil, err
			}
		}
		var existing evaluation
		err := tx.Where("candidate_id = ? AND content_digest = ?", row.ID, row.Digest).Order("attempt DESC").Take(&existing).Error
		if err == nil && (existing.Status == "passed" || existing.Status == "failed") {
			return evaluationView(&existing), nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if row.Status == "published" || row.Status == "rejected" || row.Status == "expired" {
			return nil, status.Error(codes.FailedPrecondition, "candidate is immutable")
		}
		if row.Status == "evaluating" {
			return nil, status.Error(codes.Aborted, "evaluation is in flight or interrupted; automatic replay refused")
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "legacy-skill-validation-budget").Error; err != nil {
			return nil, err
		}
		var running int64
		if err := tx.Model(&candidate{}).Where("source_review_id IS NULL AND status = 'evaluating' AND updated_at > ?", time.Now().Add(-2*time.Minute)).Count(&running).Error; err != nil {
			return nil, err
		}
		if running >= 4 {
			return nil, status.Error(codes.ResourceExhausted, "independent skill evaluation concurrency budget exhausted")
		}
		if err := tx.Model(&candidate{}).Where("id = ?", row.ID).Updates(map[string]any{"status": "evaluating", "failure": "", "updated_at": time.Now()}).Error; err != nil {
			return nil, err
		}
		claimed = true
		return map[string]string{"status": "running"}, nil
	})
	if err != nil {
		return nil, err
	}
	if !claimed {
		var state struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(prior, &state) != nil || state.Status == "running" {
			return nil, status.Error(codes.Aborted, "evaluation outcome is unknown; automatic replay refused")
		}
		return prior, nil
	}
	result, err := s.runEvaluation(ctx, bot, &row)
	if err != nil {
		result = &evaluation{CandidateID: row.ID, BotID: bot.ID, ContentDigest: row.Digest, Digest: row.Digest, TaskDigest: contentDigest(row.Task), ExpectedHash: contentDigest(row.ExpectedOutput), Status: "error", Reason: "independent model validation unavailable or interrupted"}
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.recordEvaluation(finish, bot, &row, result, request.SubmissionKey); err != nil {
		return nil, err
	}
	return marshal(evaluationView(result))
}

func (s *Service) runEvaluation(ctx context.Context, bot *botRow, row *candidate) (*evaluation, error) {
	callCtx, cancel := context.WithTimeout(ctx, evaluationTimeout)
	defer cancel()
	model, err := s.llm.SnapshotModel(callCtx, *bot.ModelID, bot.OwnerID, bot.ID)
	if err != nil {
		return nil, err
	}
	result := &evaluation{CandidateID: row.ID, BotID: bot.ID, ContentDigest: row.Digest, Digest: row.Digest, TaskDigest: contentDigest(row.Task), ExpectedHash: contentDigest(row.ExpectedOutput), ModelVersion: model.Version}
	samples := []legacySample{{Task: row.Task, Expected: row.ExpectedOutput}}
	baselineID := row.BaselineVersionID
	seen := make(map[string]bool)
	for baselineID != nil {
		if len(samples) >= 8 || seen[*baselineID] {
			result.Status, result.Reason = "insufficient", "preserved baseline suite exceeds independent validation budget or is cyclic"
			return result, nil
		}
		seen[*baselineID] = true
		var baseline version
		if err := s.db.WithContext(callCtx).Where("id = ? AND skill_id = ? AND scope_key = 'bot'", *baselineID, row.SkillID).Take(&baseline).Error; err != nil {
			return nil, err
		}
		var retained candidate
		if err := s.db.WithContext(callCtx).Where("published_version_id = ? AND skill_id = ? AND source_review_id IS NULL", *baselineID, row.SkillID).Take(&retained).Error; err != nil {
			result.Status, result.Reason = "insufficient", "original baseline representative scenario is unavailable"
			return result, nil
		}
		original := legacySample{Task: retained.Task, Expected: retained.ExpectedOutput, BaselineVersionID: retained.BaselineVersionID}
		if strings.TrimSpace(original.Task) == "" || strings.TrimSpace(original.Expected) == "" {
			result.Status, result.Reason = "insufficient", "original baseline representative scenario is unavailable"
			return result, nil
		}
		samples = append(samples, original)
		result.TaskDigest = contentDigest(result.TaskDigest + "\x00" + original.Task + "\x00" + baseline.Digest)
		result.ExpectedHash = contentDigest(result.ExpectedHash + "\x00" + original.Expected)
		baselineID = original.BaselineVersionID
	}
	inputBytes := 0
	for _, sample := range samples {
		size := len(row.Instructions) + len(sample.Task)
		inputBytes += size
		if size > 64<<10 || inputBytes > 128<<10 {
			result.Status, result.Reason = "insufficient", "preserved suite exceeds independent model input budget"
			return result, nil
		}
	}
	outputs := make([]string, 0, len(samples))
	totalTokens := int64(0)
	for _, sample := range samples {
		if strings.TrimSpace(sample.Task) == "" || strings.TrimSpace(sample.Expected) == "" {
			result.Status, result.Reason = "insufficient", "representative task requires independent exact expected output"
			return result, nil
		}
		response, err := s.llm.ChatRequest(callCtx, &llm.ChatReq{ModelId: model.ModelId, ExecutionModel: model, OwnerId: bot.OwnerID, BotId: &bot.ID, Temperature: 0, MaxTokens: 512, CacheScope: "aim-skill-evaluation:" + row.ID + ":" + row.Digest, Messages: []*llm.Message{{Role: "system", Content: row.Instructions}, {Role: "user", Content: sample.Task}}})
		if err != nil {
			return nil, err
		}
		if len(response.Choices) != 1 || response.Choices[0].Message == nil {
			result.Status, result.Reason = "error", "model returned no usable representative-task output"
			return result, nil
		}
		observed := strings.TrimSpace(response.Choices[0].Message.Content)
		outputs = append(outputs, observed)
		result.OutputDigest, result.Observed = contentDigest(strings.Join(outputs, "\x00")), bounded(observed)
		if response.Choices[0].FinishReason == "length" || len(observed) > 4096 || response.Usage != nil && response.Usage.TotalTokens > 16384 {
			result.Status, result.Reason = "error", "finite validation output or model budget exhausted"
			return result, nil
		}
		if response.Usage != nil {
			totalTokens += int64(response.Usage.TotalTokens)
		}
		if totalTokens > 32768 {
			result.Status, result.Reason = "error", "independent total validation token budget exhausted"
			return result, nil
		}
		if observed == "" {
			result.Status, result.Reason = "insufficient", "model returned no representative-task output"
			return result, nil
		}
		if observed != strings.TrimSpace(sample.Expected) {
			result.Status, result.Reason = "failed", "candidate or baseline output differs from the exact expected result"
			return result, nil
		}
	}
	result.Status, result.Reason = "passed", "candidate and original baseline output matched exact expected results"
	return result, nil
}

func (s *Service) recordEvaluation(ctx context.Context, bot *botRow, row *candidate, result *evaluation, submissionKey string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := legacyOwner(tx, bot.ID, *bot.OwnerID); err != nil {
			return err
		}
		var current candidate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Digest != row.Digest {
			return status.Error(codes.Aborted, "candidate content changed during evaluation")
		}
		if current.Status != "evaluating" {
			return status.Error(codes.Aborted, "evaluation claim no longer current")
		}
		if err := legacyBaseline(tx, &current); err != nil {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		var attempt int
		if err := tx.Model(&evaluation{}).Where("candidate_id = ? AND content_digest = ?", row.ID, row.Digest).Select("COALESCE(MAX(attempt),0)").Scan(&attempt).Error; err != nil {
			return err
		}
		result.ID, result.Attempt, result.CreatedAt = id, attempt+1, time.Now()
		digest := evaluationDigest(result)
		result.Digest = digest
		if err := tx.Create(result).Error; err != nil {
			return err
		}
		updates := map[string]any{"evaluation_digest": digest, "failure": result.Reason, "approved_digest": "", "approved_evaluation_digest": "", "approved_by": nil, "updated_at": time.Now()}
		switch result.Status {
		case "passed":
			updates["status"] = "evaluated"
		case "failed":
			updates["status"] = "validation_failed"
		default:
			updates["status"] = "pending_validation"
		}
		if err := tx.Model(&candidate{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return err
		}
		raw, err := marshal(evaluationView(result))
		if err != nil {
			return err
		}
		return tx.Model(&operationRow{}).Where("bot_id = ? AND operation = 'evaluate' AND submission_key = ?", bot.ID, submissionKey).Update("response", json.RawMessage(raw)).Error
	})
}

func evaluationDigest(result *evaluation) string {
	value := strings.Join([]string{result.Digest, result.TaskDigest, result.ExpectedHash, result.Status, result.OutputDigest, result.ModelVersion}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func evaluationView(result *evaluation) EvaluationView {
	return EvaluationView{Status: result.Status, Passed: result.Status == "passed", Reason: result.Reason, Digest: result.Digest, TaskDigest: result.TaskDigest, ExpectedDigest: result.ExpectedHash, OutputDigest: result.OutputDigest, ModelVersion: result.ModelVersion, CreatedAt: result.CreatedAt.Unix()}
}

func (s *Service) approve(ctx context.Context, bot *botRow, body []byte) ([]byte, error) {
	var request struct {
		CandidateID   string `json:"candidate_id"`
		SubmissionKey string `json:"submission_key"`
		Digest        string `json:"digest"`
	}
	if err := json.Unmarshal(body, &request); err != nil || identity.Validate(request.CandidateID) != nil || len(request.SubmissionKey) == 0 || len(request.SubmissionKey) > 256 {
		return nil, status.Error(codes.InvalidArgument, "valid candidate_id and submission_key are required")
	}
	return s.mutate(ctx, bot.ID, *bot.OwnerID, "approve", request.SubmissionKey, body, func(tx *gorm.DB) (any, error) {
		var row candidate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND bot_id = ?", request.CandidateID, bot.ID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.NotFound, "skill candidate not found")
			}
			return nil, err
		}
		if request.Digest == "" || request.Digest != row.Digest {
			return nil, status.Error(codes.Aborted, "expected candidate digest differs from current content")
		}
		if err := legacyBaseline(tx, &row); err != nil {
			return nil, err
		}
		authorityVersion, err := legacyCandidateAuthority(tx, &row, *bot.OwnerID)
		if err != nil {
			return nil, err
		}
		if row.Status == "approved" && row.ApprovedDigest == row.Digest && row.ApprovedBy != nil && *row.ApprovedBy == *bot.OwnerID && row.ApprovedAuthorizationVersion == authorityVersion {
			return viewCandidate(&row, nil)
		}
		if (row.Status != "evaluated" && row.Status != "approved") || row.EvaluationDigest == "" {
			return nil, status.Error(codes.FailedPrecondition, "candidate has no passed representative-task evaluation")
		}
		var result evaluation
		if err := tx.Where("candidate_id = ? AND digest = ? AND status = 'passed'", row.ID, row.EvaluationDigest).Take(&result).Error; err != nil {
			return nil, status.Error(codes.FailedPrecondition, "verification evidence is not a passed evaluation")
		}
		if result.ContentDigest != row.Digest {
			return nil, status.Error(codes.FailedPrecondition, "evaluation does not bind current candidate")
		}
		row.Status, row.ApprovedDigest, row.ApprovedEvaluationDigest, row.UpdatedAt = "approved", row.Digest, row.EvaluationDigest, time.Now()
		if err := tx.Model(&candidate{}).Where("id = ?", row.ID).Updates(map[string]any{"status": row.Status, "approved_digest": row.ApprovedDigest, "approved_evaluation_digest": row.ApprovedEvaluationDigest, "approved_by": *bot.OwnerID, "approved_authorization_version": authorityVersion, "updated_at": row.UpdatedAt}).Error; err != nil {
			return nil, err
		}
		return viewCandidate(&row, &result)
	})
}

func (s *Service) publish(ctx context.Context, bot *botRow, body []byte) ([]byte, error) {
	var request struct {
		CandidateID   string `json:"candidate_id"`
		SubmissionKey string `json:"submission_key"`
		Digest        string `json:"digest"`
	}
	if err := json.Unmarshal(body, &request); err != nil || identity.Validate(request.CandidateID) != nil || len(request.SubmissionKey) == 0 || len(request.SubmissionKey) > 256 {
		return nil, status.Error(codes.InvalidArgument, "valid candidate_id and submission_key are required")
	}
	return s.mutate(ctx, bot.ID, *bot.OwnerID, "publish", request.SubmissionKey, body, func(tx *gorm.DB) (any, error) {
		var row candidate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND bot_id = ?", request.CandidateID, bot.ID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, status.Error(codes.NotFound, "skill candidate not found")
			}
			return nil, err
		}
		if request.Digest == "" || request.Digest != row.Digest {
			return nil, status.Error(codes.Aborted, "expected candidate digest differs from current content")
		}
		authorityVersion, err := legacyCandidateAuthority(tx, &row, *bot.OwnerID)
		if err != nil {
			return nil, err
		}
		if row.Status == "published" && row.PublishedVersionID != nil {
			var published version
			if err := tx.Where("id = ?", *row.PublishedVersionID).Take(&published).Error; err != nil {
				return nil, err
			}
			enabled, err := VersionEnabled(tx, bot.ID, published.ID)
			return versionView(&published, enabled), err
		}
		if row.Status != "approved" || row.ApprovedDigest != row.Digest || row.ApprovedEvaluationDigest != row.EvaluationDigest || row.ApprovedBy == nil || *row.ApprovedBy != *bot.OwnerID || row.ApprovedAuthorizationVersion != authorityVersion {
			return nil, status.Error(codes.FailedPrecondition, "candidate content and verification have not been approved")
		}
		var proof evaluation
		if err := tx.Where("candidate_id = ? AND digest = ? AND content_digest = ? AND status = 'passed'", row.ID, row.EvaluationDigest, row.Digest).Take(&proof).Error; err != nil {
			return nil, status.Error(codes.FailedPrecondition, "exact candidate verification is not passed")
		}
		// Serialize version allocation per package so concurrent publishes
		// cannot race past the same baseline or reuse a version number.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-publish:"+row.SkillID).Error; err != nil {
			return nil, err
		}
		var latest version
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("skill_id = ? AND scope_key = 'bot'", row.SkillID).Order("version DESC").Take(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if (err == nil && (row.BaselineVersionID == nil || *row.BaselineVersionID != latest.ID)) || (errors.Is(err, gorm.ErrRecordNotFound) && row.BaselineVersionID != nil) {
			return nil, status.Error(codes.Aborted, "candidate baseline is stale; re-integrate, re-evaluate and re-approve")
		}
		var duplicate version
		if err := tx.Where("skill_id = ? AND digest = ? AND scope_key = 'bot'", row.SkillID, row.Digest).Take(&duplicate).Error; err == nil {
			// Identical immutable content: retry returns the existing version.
			if duplicate.Status != "published" {
				return nil, status.Error(codes.FailedPrecondition, "identical content is disabled")
			}
			enabled, err := AdoptPublished(ctx, tx, bot.ID, row.SkillID, duplicate.ID, "bot", *bot.OwnerID)
			if err != nil {
				return nil, err
			}
			if err := tx.Model(&candidate{}).Where("id = ?", row.ID).Updates(map[string]any{"status": "published", "published_version_id": duplicate.ID, "updated_at": time.Now()}).Error; err != nil {
				return nil, err
			}
			return versionView(&duplicate, enabled), nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		id, err := identity.New()
		if err != nil {
			return nil, err
		}
		var maximum int
		if err := tx.Model(&version{}).Where("skill_id = ?", row.SkillID).Select("COALESCE(MAX(version),0)").Scan(&maximum).Error; err != nil {
			return nil, err
		}
		contract, err := json.Marshal(map[string]any{"id": row.ID, "digest": row.Digest, "baseline_version_id": row.BaselineVersionID, "io_contract_digest": contentDigest(row.Task + "\x00" + row.ExpectedOutput), "evaluation": map[string]any{"status": proof.Status, "digest": proof.Digest, "task_digest": proof.TaskDigest, "expected_digest": proof.ExpectedHash, "output_digest": proof.OutputDigest, "model_version": proof.ModelVersion}})
		if err != nil {
			return nil, err
		}
		contract, err = StampContract(contract, map[string]any{"mode": "instruction-only", "tools": false}, map[string]any{"scope": "bot"})
		if err != nil {
			return nil, err
		}
		created := version{ID: id, SkillID: row.SkillID, BotID: bot.ID, ScopeKey: "bot", Version: maximum + 1, Digest: row.Digest, Name: row.Name, Description: row.Description, Instructions: row.Instructions, Resources: row.Resources, License: row.License, Status: "published", ValidationContract: contract, CreatedAt: time.Now()}
		if err := tx.Create(&created).Error; err != nil {
			return nil, err
		}
		enabled, err := AdoptPublished(ctx, tx, bot.ID, row.SkillID, created.ID, "bot", *bot.OwnerID)
		if err != nil {
			return nil, err
		}
		if err := tx.Model(&candidate{}).Where("id = ?", row.ID).Updates(map[string]any{"status": "published", "published_version_id": created.ID, "updated_at": time.Now()}).Error; err != nil {
			return nil, err
		}
		return versionView(&created, enabled), nil
	})
}

// mutate records a stable submission identity with its response. A retry
// replays the recorded outcome; the same key with different content conflicts.
func (s *Service) mutate(ctx context.Context, botID, caller, operation, key string, body []byte, fn func(tx *gorm.DB) (any, error)) ([]byte, error) {
	hash := sha256.Sum256(body)
	requestHash := hex.EncodeToString(hash[:])
	var response []byte
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := legacyOwner(tx, botID, caller); err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill:"+botID+":"+operation+":"+key).Error; err != nil {
			return err
		}
		var prior operationRow
		err := tx.Where("bot_id = ? AND operation = ? AND submission_key = ?", botID, operation, key).Take(&prior).Error
		if err == nil {
			if prior.RequestHash != requestHash {
				return status.Error(codes.AlreadyExists, "submission key has different content")
			}
			response = prior.Response
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result, err := fn(tx)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		if err := tx.Create(&operationRow{ID: id, BotID: botID, Operation: operation, SubmissionKey: key, RequestHash: requestHash, Response: raw, CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
		response = raw
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func marshal(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, status.Error(codes.Internal, "serialize skill response failed")
	}
	return raw, nil
}

func bounded(value string) string {
	if len(value) <= maxObservedBytes {
		return value
	}
	cut := maxObservedBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func versionView(row *version, enabled bool) *VersionView {
	return &VersionView{VersionID: row.ID, Version: row.Version, Name: row.Name, Description: row.Description, Digest: row.Digest, Status: row.Status, Enabled: enabled, CreatedAt: row.CreatedAt.Unix()}
}

// enabledVersions is the only catalog a Run may freeze. Explicit selection is
// a subset of this set; nothing in the package can widen it.
func (s *Service) enabledVersions(ctx context.Context, botID string) ([]version, error) {
	return s.enabledVersionsScoped(ctx, botID, "bot")
}

func (s *Service) enabledVersionsScoped(ctx context.Context, botID, scopeKey string) ([]version, error) {
	var rows []version
	err := s.db.WithContext(ctx).Table("bot.skill_versions AS v").
		Joins("JOIN bot.skill_bindings AS b ON b.version_id = v.id AND b.skill_id = v.skill_id").
		Joins("JOIN bot.skill_packages AS p ON p.id = v.skill_id").
		Select("v.*").
		Where("b.bot_id = ? AND b.enabled = true AND v.status = 'published' AND v.bot_id = ? AND p.bot_id = ? AND v.scope_key IN ? AND b.scope_key = v.scope_key", botID, botID, botID, []string{"bot", scopeKey}).
		Order("v.name ASC, v.version DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// Freeze snapshots the enabled catalog identity, not the instruction bodies.
// Bodies and resources remain progressively loadable under current authority.
func Freeze(ctx context.Context, db *gorm.DB, botID string, explicit []string) ([]Frozen, error) {
	return FreezeScoped(ctx, db, botID, explicit, "bot")
}

// FreezeScoped includes source-derived versions only in their exact accepted
// Run scope. Callers must additionally verify current source-material access.
func FreezeScoped(ctx context.Context, db *gorm.DB, botID string, explicit []string, scopeKey string) ([]Frozen, error) {
	service := &Service{db: db}
	rows, err := service.enabledVersionsScoped(ctx, botID, scopeKey)
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]version, len(rows))
	for _, row := range rows {
		enabled[row.ID] = row
	}
	selected := make(map[string]bool, len(explicit))
	for _, id := range explicit {
		if identity.Validate(id) != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid skill version identity")
		}
		if _, ok := enabled[id]; !ok {
			return nil, status.Error(codes.PermissionDenied, "selected skill is not enabled and published for this Bot")
		}
		selected[id] = true
	}
	if len(selected) > 8 {
		return nil, status.Error(codes.InvalidArgument, "at most 8 skills may be selected explicitly")
	}
	result := make([]Frozen, 0, len(rows))
	for _, row := range rows {
		var pkg packageRow
		if err := db.WithContext(ctx).Where("id = ?", row.SkillID).Take(&pkg).Error; err != nil {
			return nil, err
		}
		result = append(result, Frozen{VersionID: row.ID, PackageID: row.SkillID, PublisherID: pkg.PublisherID, ScopeID: botID, ScopeKey: row.ScopeKey, Version: row.Version, Name: row.Name, Description: row.Description, Digest: row.Digest, Explicit: selected[row.ID]})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].VersionID < result[j].VersionID
	})
	return result, nil
}

// Check revalidates current Bot and enablement state. Publishing a newer
// compatible version does not revoke an old frozen version; disabling the
// skill does.
func Check(ctx context.Context, db *gorm.DB, botID string, frozen Frozen) error {
	if frozen.ScopeID != botID {
		return status.Error(codes.PermissionDenied, "skill version belongs to another scope")
	}
	var bot botRow
	if err := db.WithContext(ctx).Table("bots").Where("id = ?", botID).Take(&bot).Error; err != nil {
		return status.Error(codes.PermissionDenied, "Bot is not available")
	}
	if bot.Status != "active" {
		return status.Error(codes.PermissionDenied, "Bot is disabled")
	}
	var count int64
	scopeKey := frozen.ScopeKey
	if scopeKey == "" {
		scopeKey = "bot"
	}
	err := db.WithContext(ctx).Model(&version{}).
		Joins("JOIN bot.skill_bindings AS b ON b.skill_id = skill_versions.skill_id").
		Where("skill_versions.id = ? AND skill_versions.bot_id = ? AND skill_versions.status = 'published' AND skill_versions.scope_key = ? AND b.scope_key = skill_versions.scope_key AND b.bot_id = ?", frozen.VersionID, botID, scopeKey, botID).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return status.Error(codes.PermissionDenied, "skill version is no longer enabled")
	}
	return nil
}

// Read returns SKILL.md or one resource from the frozen version. It never reads
// a newer version and never accepts a caller-derived path beyond exact keys.
func Read(ctx context.Context, db *gorm.DB, botID string, frozen Frozen, resource string) (string, error) {
	if resource != "SKILL.md" && !(strings.HasPrefix(resource, "references/") || strings.HasPrefix(resource, "assets/")) {
		return "", status.Error(codes.InvalidArgument, "resource must be SKILL.md, references/* or assets/*")
	}
	if err := Check(ctx, db, botID, frozen); err != nil {
		return "", err
	}
	var row version
	if err := db.WithContext(ctx).Where("id = ? AND bot_id = ?", frozen.VersionID, botID).Take(&row).Error; err != nil {
		return "", status.Error(codes.NotFound, "skill version not found")
	}
	if row.Digest != frozen.Digest {
		return "", status.Error(codes.FailedPrecondition, "skill version identity does not match the frozen Run")
	}
	if resource == "SKILL.md" {
		return row.Instructions, nil
	}
	resources := map[string]string{}
	if err := json.Unmarshal(row.Resources, &resources); err != nil {
		return "", status.Error(codes.FailedPrecondition, "stored skill resources are invalid")
	}
	content, ok := resources[resource]
	if !ok {
		return "", status.Error(codes.NotFound, fmt.Sprintf("skill resource %q not found", resource))
	}
	return content, nil
}
