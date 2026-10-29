package skill

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/skillvalidation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// ExecutionContract is the immutable, published admission identity for a fixed
// script. It is not a permission: every invocation also checks current
// enablement, exact frozen digest, material authority and the manager's root
// policy. A published contract never executes package-declared code.
type ExecutionContract struct {
	Identity           skillvalidation.Identity
	CodeReviewDigest   string
	CodeReviewIdentity string
}

type storedExecutionContract struct {
	Digest             string            `json:"digest"`
	Scripts            map[string]string `json:"scripts"`
	ImageDigest        string            `json:"image_digest"`
	EntryPoint         string            `json:"entry_point"`
	CodeReviewDigest   string            `json:"code_review_digest"`
	CodeReviewIdentity string            `json:"code_review_identity"`
	Evaluation         struct {
		Status             string `json:"status"`
		Passed             bool   `json:"passed"`
		Digest             string `json:"digest"`
		CodeReviewIdentity string `json:"code_review_identity"`
	} `json:"evaluation"`
}

// LoadExecution returns the exact published script/image/entry identity only
// when its trusted review and passed validation are still bound to this version
// content. It never substitutes a newer version or a caller-supplied script.
func LoadExecution(ctx context.Context, db *gorm.DB, botID string, frozen Frozen) (*ExecutionContract, error) {
	if err := Check(ctx, db, botID, frozen); err != nil {
		return nil, err
	}
	var row struct {
		ID                 string
		Digest             string
		ValidationContract json.RawMessage
	}
	if err := db.WithContext(ctx).Table("bot.skill_versions").Select("id, digest, validation_contract").Where("id = ? AND bot_id = ?", frozen.VersionID, botID).Take(&row).Error; err != nil {
		return nil, status.Error(codes.NotFound, "skill version not found")
	}
	if row.Digest != frozen.Digest {
		return nil, status.Error(codes.FailedPrecondition, "skill version identity does not match the frozen Run")
	}
	var contract storedExecutionContract
	if json.Unmarshal(row.ValidationContract, &contract) != nil || len(contract.Scripts) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "skill version has no reviewed fixed script")
	}
	identity := skillvalidation.Identity{Scripts: contract.Scripts, ImageDigest: contract.ImageDigest, EntryPoint: contract.EntryPoint}
	raw, err := json.Marshal(identity)
	if err != nil {
		return nil, err
	}
	if contract.CodeReviewIdentity == "" || contract.CodeReviewDigest != contentDigest(string(raw)) || contract.Evaluation.Status != "passed" || !contract.Evaluation.Passed || contract.Evaluation.Digest != row.Digest || contract.Evaluation.CodeReviewIdentity != contract.CodeReviewIdentity {
		return nil, status.Error(codes.FailedPrecondition, "skill script review and passed validation are not bound to this version")
	}
	return &ExecutionContract{Identity: identity, CodeReviewDigest: contract.CodeReviewDigest, CodeReviewIdentity: contract.CodeReviewIdentity}, nil
}

// executionCapable discloses only that a published contract contains a fixed
// script. It does not imply current admission, material authority or a permit.
func executionCapable(raw json.RawMessage) bool {
	var contract storedExecutionContract
	return json.Unmarshal(raw, &contract) == nil && len(contract.Scripts) > 0 && contract.CodeReviewIdentity != ""
}
