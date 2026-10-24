package run

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/app/bot-service/internal/skill"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type reviewBaseline struct {
	ID           string          `json:"version_id"`
	SkillID      string          `json:"skill_id"`
	Instructions string          `json:"instructions"`
	Resources    json.RawMessage `json:"resources"`
}

// An authorized catalog version may be a modification baseline without claiming
// that this Run actually used it. Caller-selected arbitrary UUIDs are rejected.
func reviewTarget(ctx context.Context, db *gorm.DB, r *model.Run, review *runReview, id string) (*reviewBaseline, error) {
	var config ExecutionConfig
	if err := json.Unmarshal(r.Config, &config); err != nil {
		return nil, err
	}
	frozen, ok := frozenSkill(config, id)
	if !ok || review.PublisherID == nil {
		return nil, status.Error(codes.FailedPrecondition, "target is outside the authorized frozen skill catalog")
	}
	body, err := skill.Read(ctx, db, r.BotID, frozen, "SKILL.md")
	if err != nil {
		return nil, err
	}
	var baseline reviewBaseline
	if err := db.WithContext(ctx).Table("bot.skill_versions v").Select("v.id,v.skill_id,v.resources").Joins("JOIN bot.skill_packages p ON p.id = v.skill_id").Where("v.id = ? AND v.bot_id = ? AND p.publisher_id = ? AND p.scope_key IN ?", id, r.BotID, *review.PublisherID, []string{"bot", review.ScopeKey}).Take(&baseline).Error; err != nil {
		return nil, status.Error(codes.FailedPrecondition, "target publisher or source scope is unavailable")
	}
	baseline.Instructions = body
	return &baseline, nil
}
