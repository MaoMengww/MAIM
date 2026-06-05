package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/maomeng/aim/pkg/database"
)

// PgRepo implements Store using PostgreSQL.
type PgRepo struct {
	db *database.DB
}

// NewPgRepo creates a new PgRepo.
func NewPgRepo(db *database.DB) *PgRepo {
	return &PgRepo{db: db}
}

func (r *PgRepo) Insert(ctx context.Context, item *MemoryItem) error {
	// Dedup: find existing fact with similar content for this user+bot
	similar, err := r.findSimilar(ctx, item)
	if err != nil {
		return err
	}
	if similar != nil {
		return r.mergeFact(ctx, similar, item)
	}

	// New fact
	return r.db.WithContext(ctx).Create(item).Error
}

// mergeFact merges a new fact into an existing one, taking max scores and updating timestamps.
func (r *PgRepo) mergeFact(ctx context.Context, existing *MemoryItem, item *MemoryItem) error {
	existing.Importance = math.Max(existing.Importance, item.Importance)
	existing.Confidence = math.Max(existing.Confidence, item.Confidence)
	existing.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Save(existing).Error
}

// findSimilar checks for existing facts with content similar to the given item.
func (r *PgRepo) findSimilar(ctx context.Context, item *MemoryItem) (*MemoryItem, error) {
	var existing []MemoryItem
	err := r.db.WithContext(ctx).
		Where("bot_id = ? AND user_id = ? AND memory_type = ?", item.BotID, item.UserID, item.MemoryType).
		Find(&existing).Error
	if err != nil {
		return nil, err
	}
	for i := range existing {
		if s := &existing[i]; contentSimilarity(s.Content, item.Content) > 0.65 {
			return s, nil
		}
	}
	return nil, nil
}

// contentSimilarity computes Jaccard similarity over runes between two strings.
func contentSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	setA := make(map[rune]struct{})
	for _, r := range a {
		if !unicode.IsSpace(r) {
			setA[r] = struct{}{}
		}
	}
	if len(setA) == 0 {
		return 0.0
	}
	intersect := 0
	seen := make(map[rune]bool)
	for _, r := range b {
		if unicode.IsSpace(r) {
			continue
		}
		if _, ok := setA[r]; ok && !seen[r] {
			intersect++
			seen[r] = true
		}
	}
	union := len(setA)
	for _, r := range b {
		if unicode.IsSpace(r) {
			continue
		}
		if !seen[r] {
			union++
		}
	}
	if union == 0 {
		return 0.0
	}
	return float64(intersect) / float64(union)
}

// addFinalScores computes final_score for each memory using the unified formula
// and sorts them descending by score.
func addFinalScores(items []*MemoryItem, maxAccess int) {
	now := time.Now()
	for _, m := range items {
		m.FinalScore = ComputeScore(m, now, maxAccess)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].FinalScore > items[j].FinalScore
	})
}

func (r *PgRepo) Update(ctx context.Context, item *MemoryItem) error {
	return r.db.WithContext(ctx).Model(&MemoryItem{}).Where("id = ?", item.ID).Updates(map[string]any{
		"content":          item.Content,
		"subject":          item.Subject,
		"predicate":        item.Predicate,
		"object":           item.Object,
		"category":         item.Category,
		"importance":       item.Importance,
		"confidence":       item.Confidence,
		"access_count":     item.AccessCount,
		"last_accessed_at": item.LastAccessedAt,
	}).Error
}

func (r *PgRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&MemoryItem{}).Error
}

func (r *PgRepo) DeleteByUser(ctx context.Context, botID, userID int64) error {
	return r.db.WithContext(ctx).Where("bot_id = ? AND user_id = ?", botID, userID).Delete(&MemoryItem{}).Error
}

func (r *PgRepo) FindByID(ctx context.Context, id int64) (*MemoryItem, error) {
	var m MemoryItem
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *PgRepo) FindByUser(ctx context.Context, botID, userID int64, limit int) ([]*MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}

	// Get maxAccess for frequency normalization
	var maxAccess int
	r.db.WithContext(ctx).Model(&MemoryItem{}).
		Where("bot_id = ? AND user_id = ?", botID, userID).
		Select("COALESCE(MAX(access_count), 0)").
		Scan(&maxAccess)

	var items []*MemoryItem
	err := r.db.WithContext(ctx).
		Where("bot_id = ? AND user_id = ?", botID, userID).
		Find(&items).Error
	if err != nil {
		return nil, err
	}

	addFinalScores(items, maxAccess)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *PgRepo) CountByUser(ctx context.Context, botID, userID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&MemoryItem{}).
		Where("bot_id = ? AND user_id = ?", botID, userID).
		Count(&count).Error
	return count, err
}

// SearchByUser finds memories matching a query for a user-bot pair.
func (r *PgRepo) SearchByUser(ctx context.Context, botID, userID int64, query string, limit int) ([]*MemoryItem, error) {
	if limit <= 0 {
		limit = 20
	}

	// Get maxAccess for frequency normalization
	var maxAccess int
	r.db.WithContext(ctx).Model(&MemoryItem{}).
		Where("bot_id = ? AND user_id = ?", botID, userID).
		Select("COALESCE(MAX(access_count), 0)").
		Scan(&maxAccess)

	var items []*MemoryItem
	db := r.db.WithContext(ctx).Where("bot_id = ? AND user_id = ?", botID, userID)
	if query != "" {
		keyword := "%" + strings.ToLower(query) + "%"
		db = db.Where("LOWER(content) LIKE ? OR LOWER(subject) LIKE ? OR LOWER(object) LIKE ?",
			keyword, keyword, keyword)
	}
	err := db.Find(&items).Error
	if err != nil {
		return nil, err
	}

	addFinalScores(items, maxAccess)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// TouchBatch updates access_count and last_accessed_at for multiple memories.
func (r *PgRepo) TouchBatch(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return r.db.WithContext(ctx).Model(&MemoryItem{}).
		Where("id IN ?", ids).
		Updates(map[string]any{
			"access_count":     gormInc(1),
			"last_accessed_at": now,
		}).Error
}

// gormInc returns a raw SQL expression for incrementing a column.
func gormInc(n int) interface{} {
	return fmt.Sprintf("access_count + %d", n)
}

// TableName overrides the default table name.
func (MemoryItem) TableName() string {
	return "bot_memories"
}
