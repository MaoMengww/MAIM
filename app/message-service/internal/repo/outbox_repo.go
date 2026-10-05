package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type OutboxRepo struct {
	db *database.DB
}

func NewOutboxRepo(db *database.DB) *OutboxRepo {
	return &OutboxRepo{db: db}
}

func (r *OutboxRepo) Insert(ctx context.Context, tx *gorm.DB, event *model.OutboxEvent) error {
	return tx.WithContext(ctx).Create(event).Error
}

// InTransaction keeps publication row locks until Kafka sends and their status
// changes commit, preventing another dispatcher from overtaking this batch.
func (r *OutboxRepo) InTransaction(ctx context.Context, fn func(*OutboxRepo) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewOutboxRepo(&database.DB{DB: tx}))
	})
}

func (r *OutboxRepo) FetchPending(ctx context.Context, limit int) ([]model.OutboxEvent, error) {
	var events []model.OutboxEvent
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", model.OutboxStatusPending, time.Now()).
		Where(`NOT EXISTS (SELECT 1 FROM messaging.outbox_events older
			WHERE older.topic = outbox_events.topic AND older.key = outbox_events.key
			AND older.status <> ? AND (older.created_at, older.id) < (outbox_events.created_at, outbox_events.id))`, model.OutboxStatusSent).
		Order("created_at ASC, id ASC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

func (r *OutboxRepo) MarkSent(ctx context.Context, id int64) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&model.OutboxEvent{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        model.OutboxStatusSent,
			"dispatched_at": now,
		}).Error
}

func (r *OutboxRepo) MarkRetry(ctx context.Context, id int64, nextRetryAt time.Time, lastError string) error {
	return r.db.WithContext(ctx).
		Model(&model.OutboxEvent{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"next_retry_at": nextRetryAt,
			"last_error":    lastError,
			"retry_count":   gorm.Expr("retry_count + 1"),
		}).Error
}

func (r *OutboxRepo) MarkFailed(ctx context.Context, id int64, lastError string) error {
	return r.db.WithContext(ctx).
		Model(&model.OutboxEvent{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      model.OutboxStatusFailed,
			"last_error":  lastError,
			"retry_count": gorm.Expr("retry_count + 1"),
		}).Error
}

func (r *OutboxRepo) DeleteSentBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("status = ? AND dispatched_at < ?", model.OutboxStatusSent, before).
		Limit(limit).
		Delete(&model.OutboxEvent{})
	return result.RowsAffected, result.Error
}

func (r *OutboxRepo) CountByStatus(ctx context.Context, status int16) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.OutboxEvent{}).
		Where("status = ?", status).
		Count(&count).Error
	return count, err
}
