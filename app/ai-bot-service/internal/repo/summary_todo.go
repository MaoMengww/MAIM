package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/pkg/database"
)

type SummaryTodo struct {
	ID        int64     `gorm:"primaryKey"`
	SummaryID int64     `gorm:"column:summary_id"`
	ConvID    int64     `gorm:"column:conv_id"`
	Content   string    `gorm:"column:content"`
	Done      bool      `gorm:"column:done"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (SummaryTodo) TableName() string { return "bot.summary_todos" }

type SummaryTodoRepo struct {
	db *database.DB
}

func NewSummaryTodoRepo(db *database.DB) *SummaryTodoRepo {
	return &SummaryTodoRepo{db: db}
}

func (r *SummaryTodoRepo) Create(ctx context.Context, t *SummaryTodo) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *SummaryTodoRepo) FindBySummary(ctx context.Context, summaryID int64) ([]SummaryTodo, error) {
	var items []SummaryTodo
	err := r.db.WithContext(ctx).
		Where("summary_id = ?", summaryID).
		Order("created_at ASC").
		Find(&items).Error
	return items, err
}

func (r *SummaryTodoRepo) Update(ctx context.Context, todoID int64, content string, done bool) error {
	updates := map[string]any{"updated_at": time.Now()}
	if content != "" {
		updates["content"] = content
	}
	updates["done"] = done
	return r.db.WithContext(ctx).Model(&SummaryTodo{}).Where("id = ?", todoID).Updates(updates).Error
}

func (r *SummaryTodoRepo) Delete(ctx context.Context, todoID int64) error {
	return r.db.WithContext(ctx).Delete(&SummaryTodo{}, todoID).Error
}
