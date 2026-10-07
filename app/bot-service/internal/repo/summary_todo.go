package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
)

type SummaryTodo struct {
	ID        string    `gorm:"primaryKey;type:uuid"`
	SummaryID *string   `gorm:"column:summary_id;type:uuid"`
	ConvID    string    `gorm:"column:conv_id;type:uuid"`
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
	id, err := identity.New()
	if err != nil {
		return err
	}
	t.ID = id
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *SummaryTodoRepo) FindBySummary(ctx context.Context, summaryID string) ([]SummaryTodo, error) {
	var items []SummaryTodo
	err := r.db.WithContext(ctx).
		Where("summary_id = ?", summaryID).
		Order("created_at ASC").
		Find(&items).Error
	return items, err
}

func (r *SummaryTodoRepo) Update(ctx context.Context, todoID, convID string, content *string, done *bool) error {
	updates := map[string]any{"updated_at": time.Now()}
	if content != nil {
		updates["content"] = *content
	}
	if done != nil {
		updates["done"] = *done
	}
	return r.db.WithContext(ctx).Model(&SummaryTodo{}).Where("id = ? AND conv_id = ?", todoID, convID).Updates(updates).Error
}

func (r *SummaryTodoRepo) Delete(ctx context.Context, todoID, convID string) error {
	return r.db.WithContext(ctx).Where("id = ? AND conv_id = ?", todoID, convID).Delete(&SummaryTodo{}).Error
}

func (r *SummaryTodoRepo) Get(ctx context.Context, todoID string) (*SummaryTodo, error) {
	var todo SummaryTodo
	if err := r.db.WithContext(ctx).Where("id = ?", todoID).First(&todo).Error; err != nil {
		return nil, err
	}
	return &todo, nil
}

func (r *SummaryTodoRepo) FindStandaloneByConv(ctx context.Context, convID string) ([]SummaryTodo, error) {
	var items []SummaryTodo
	err := r.db.WithContext(ctx).Where("conv_id = ? AND summary_id IS NULL", convID).Order("created_at ASC").Find(&items).Error
	return items, err
}
