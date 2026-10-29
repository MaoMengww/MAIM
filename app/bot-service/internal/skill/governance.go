package skill

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type legacySample struct {
	Task              string  `json:"representative_task"`
	BaselineVersionID *string `json:"baseline_version_id,omitempty"`
	Expected          string  `json:"expected_output"`
}

// The transaction, rather than the initial HTTP read or a cached operation
// response, pins current management authority through the mutation boundary.
func legacyOwner(tx *gorm.DB, botID, caller string) error {
	var current botRow
	if err := tx.Table("bots").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", botID).Take(&current).Error; err != nil {
		return err
	}
	if current.Status != "active" || current.OwnerType != "user" || current.OwnerID == nil || *current.OwnerID != caller {
		return status.Error(codes.PermissionDenied, "current active Bot owner permission required")
	}
	return nil
}

func legacyBaseline(tx *gorm.DB, row *candidate) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "skill-publish:"+row.SkillID).Error; err != nil {
		return err
	}
	var latest version
	err := tx.Where("skill_id = ? AND scope_key = 'bot'", row.SkillID).Order("version DESC").Take(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if (err == nil && (row.BaselineVersionID == nil || *row.BaselineVersionID != latest.ID)) || (errors.Is(err, gorm.ErrRecordNotFound) && row.BaselineVersionID != nil) {
		return status.Error(codes.Aborted, "candidate baseline is stale; re-integrate and re-evaluate")
	}
	return nil
}

func legacyCandidateAuthority(tx *gorm.DB, row *candidate, caller string) (int64, error) {
	var governed int64
	if err := tx.Table("bot.skill_candidates").Where("id = ? AND review_content IS NOT NULL", row.ID).Count(&governed).Error; err != nil {
		return 0, err
	}
	if governed != 0 {
		return 0, status.Error(codes.PermissionDenied, "reviewed candidate requires candidate governance")
	}
	var bot botRow
	if err := tx.Table("bots").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", row.BotID).Take(&bot).Error; err != nil {
		return 0, err
	}
	var pkg packageRow
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND scope_key = 'bot'", row.SkillID, row.BotID).Take(&pkg).Error; err != nil {
		return 0, err
	}
	if bot.OwnerType != "user" || bot.OwnerID == nil || *bot.OwnerID != caller || pkg.PublisherID != caller {
		return 0, status.Error(codes.PermissionDenied, "current package publisher authority required")
	}
	return bot.AuthorizationVersion, nil
}
