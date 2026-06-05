package repo

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/maomeng/aim/app/audit-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type AuditRepo struct {
	db *database.DB
}

func NewAuditRepo(db *database.DB) *AuditRepo {
	return &AuditRepo{db: db}
}

func (r *AuditRepo) Insert(ctx context.Context, event *model.AuditEvent) error {
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *AuditRepo) BatchInsert(ctx context.Context, events []*model.AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(events, 500).Error
}

func (r *AuditRepo) GetByEventID(ctx context.Context, eventID string) (*model.AuditEvent, error) {
	var event model.AuditEvent
	err := r.db.WithContext(ctx).
		Where("event_id = ?", eventID).
		Where("is_archived = false").
		First(&event).Error
	if err != nil {
		return nil, err
	}
	return &event, nil
}

type ListFilter struct {
	UserID       *int64
	Action       *int32
	Result       *int32
	Risk         *int32
	ResourceType *string
	ResourceID   *string
	StartTime    *int64
	EndTime      *int64
	IPAddress    *string
	ServiceName  *string
	Page         int
	PageSize     int
}

type ListResult struct {
	Events    []model.AuditEvent
	Total     int64
	Page      int
	PageSize  int
	TotalPage int
}

func (r *AuditRepo) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	q := r.db.WithContext(ctx).Model(&model.AuditEvent{}).Where("is_archived = false")

	if f.UserID != nil {
		q = q.Where("user_id = ?", *f.UserID)
	}
	if f.Action != nil {
		q = q.Where("action = ?", *f.Action)
	}
	if f.Result != nil {
		q = q.Where("result = ?", *f.Result)
	}
	if f.Risk != nil {
		q = q.Where("risk = ?", *f.Risk)
	}
	if f.ResourceType != nil && *f.ResourceType != "" {
		q = q.Where("resource_type = ?", *f.ResourceType)
	}
	if f.ResourceID != nil && *f.ResourceID != "" {
		q = q.Where("resource_id = ?", *f.ResourceID)
	}
	if f.StartTime != nil && *f.StartTime > 0 {
		q = q.Where("created_at >= to_timestamp(?)", *f.StartTime)
	}
	if f.EndTime != nil && *f.EndTime > 0 {
		q = q.Where("created_at <= to_timestamp(?)", *f.EndTime)
	}
	if f.IPAddress != nil && *f.IPAddress != "" {
		q = q.Where("ip_address = ?", *f.IPAddress)
	}
	if f.ServiceName != nil && *f.ServiceName != "" {
		q = q.Where("service_name = ?", *f.ServiceName)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count audit events: %w", err)
	}

	if f.PageSize <= 0 {
		f.PageSize = 20
	}
	if f.Page <= 0 {
		f.Page = 1
	}

	var events []model.AuditEvent
	offset := (f.Page - 1) * f.PageSize
	if err := q.Order("created_at DESC").Offset(offset).Limit(f.PageSize).Find(&events).Error; err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}

	totalPage := int(math.Ceil(float64(total) / float64(f.PageSize)))

	return &ListResult{
		Events:    events,
		Total:     total,
		Page:      f.Page,
		PageSize:  f.PageSize,
		TotalPage: totalPage,
	}, nil
}

func (r *AuditRepo) Export(ctx context.Context, f ListFilter) ([]model.AuditEvent, error) {
	q := r.db.WithContext(ctx).Model(&model.AuditEvent{}).Where("is_archived = false")

	if f.UserID != nil {
		q = q.Where("user_id = ?", *f.UserID)
	}
	if f.Action != nil {
		q = q.Where("action = ?", *f.Action)
	}
	if f.Result != nil {
		q = q.Where("result = ?", *f.Result)
	}
	if f.StartTime != nil && *f.StartTime > 0 {
		q = q.Where("created_at >= to_timestamp(?)", *f.StartTime)
	}
	if f.EndTime != nil && *f.EndTime > 0 {
		q = q.Where("created_at <= to_timestamp(?)", *f.EndTime)
	}
	if f.ServiceName != nil && *f.ServiceName != "" {
		q = q.Where("service_name = ?", *f.ServiceName)
	}

	var events []model.AuditEvent
	if err := q.Order("created_at DESC").Limit(f.PageSize).Find(&events).Error; err != nil {
		return nil, fmt.Errorf("export audit events: %w", err)
	}
	return events, nil
}

func (r *AuditRepo) Archive(ctx context.Context, beforeTime int64, deleteAfter bool) (int64, error) {
	t := time.Unix(beforeTime, 0)
	q := r.db.WithContext(ctx).Model(&model.AuditEvent{}).
		Where("created_at < ?", t).
		Where("is_archived = false")

	if deleteAfter {
		result := q.Delete(&model.AuditEvent{})
		return result.RowsAffected, result.Error
	}
	result := q.Update("is_archived", true)
	return result.RowsAffected, result.Error
}

func (r *AuditRepo) UpdateReviewResult(ctx context.Context, eventID string, status int32, result map[string]any) error {
	rv := model.JSONMap(result)
	return r.db.WithContext(ctx).Model(&model.AuditEvent{}).
		Where("event_id = ?", eventID).
		Updates(map[string]any{
			"review_status": status,
			"review_result": rv,
		}).Error
}
