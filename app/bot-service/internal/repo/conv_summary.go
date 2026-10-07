package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
)

type ConvSummary struct {
	ID           string    `gorm:"primaryKey;type:uuid"`
	ConvID       string    `gorm:"column:conv_id;type:uuid"`
	UserID       string    `gorm:"column:user_id;type:uuid"`
	RangeType    string    `gorm:"column:range_type"`
	RangeStart   int64     `gorm:"column:range_start"`
	RangeEnd     int64     `gorm:"column:range_end"`
	MessageCount int       `gorm:"column:message_count"`
	Summary      string    `gorm:"column:summary"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (ConvSummary) TableName() string { return "bot.conv_summaries" }

type ConvSummaryRepo struct {
	db *database.DB
}

func NewConvSummaryRepo(db *database.DB) *ConvSummaryRepo {
	return &ConvSummaryRepo{db: db}
}

func (r *ConvSummaryRepo) Create(ctx context.Context, s *ConvSummary) error {
	id, err := identity.New()
	if err != nil {
		return err
	}
	s.ID = id
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *ConvSummaryRepo) FindByConv(ctx context.Context, convID string, limit int) ([]ConvSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	var items []ConvSummary
	err := r.db.WithContext(ctx).
		Where("conv_id = ?", convID).
		Order("created_at DESC").
		Limit(limit).
		Find(&items).Error
	return items, err
}

func (r *ConvSummaryRepo) Get(ctx context.Context, summaryID string) (*ConvSummary, error) {
	var summary ConvSummary
	if err := r.db.WithContext(ctx).Where("id = ?", summaryID).First(&summary).Error; err != nil {
		return nil, err
	}
	return &summary, nil
}
