package repo

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type MessageRepo struct {
	db *database.DB
}

func NewMessageRepo(db *database.DB) *MessageRepo {
	return &MessageRepo{db: db}
}

func (r *MessageRepo) Insert(ctx context.Context, msg *model.Message) error {
	return r.db.WithContext(ctx).Create(msg).Error
}

func (r *MessageRepo) GetByID(ctx context.Context, id int64) (*model.Message, error) {
	var msg model.Message
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&msg).Error
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

func (r *MessageRepo) GetByIDs(ctx context.Context, ids []int64) ([]model.Message, error) {
	var msgs []model.Message
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (r *MessageRepo) BatchGetByIDs(ctx context.Context, ids []int64) ([]model.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return r.GetByIDs(ctx, ids)
}

func (r *MessageRepo) GetByConvID(ctx context.Context, convID int64, limit int, cursor int64, beforeTime, afterTime int64, filterTypes []int32) ([]model.Message, error) {
	q := r.db.WithContext(ctx).Where("conv_id = ?", convID)

	if cursor > 0 {
		q = q.Where("seq < ?", cursor)
	}
	if beforeTime > 0 {
		q = q.Where("created_at <= to_timestamp(?)", beforeTime)
	}
	if afterTime > 0 {
		q = q.Where("created_at >= to_timestamp(?)", afterTime)
	}
	if len(filterTypes) > 0 {
		q = q.Where("msg_type IN ?", filterTypes)
	}

	var msgs []model.Message
	err := q.Order("seq DESC").Limit(limit).Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (r *MessageRepo) GetAroundSeq(ctx context.Context, convID int64, seq int64, limit int32) ([]model.Message, error) {
	half := int64(limit / 2)
	var msgs []model.Message
	err := r.db.WithContext(ctx).
		Where("conv_id = ? AND seq BETWEEN ? AND ?", convID, seq-half, seq+half).
		Order("seq ASC").
		Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (r *MessageRepo) GetByConvIDAndSeq(ctx context.Context, convID int64, fromSeq int64, limit int32) ([]model.Message, error) {
	var msgs []model.Message
	err := r.db.WithContext(ctx).
		Where("conv_id = ? AND seq > ?", convID, fromSeq).
		Order("seq ASC").
		Limit(int(limit)).
		Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (r *MessageRepo) GetMaxSeq(ctx context.Context, convID int64) (int64, error) {
	var seq int64
	err := r.db.WithContext(ctx).
		Model(&model.Message{}).
		Where("conv_id = ?", convID).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&seq).Error
	return seq, err
}

func (r *MessageRepo) UpdateContent(ctx context.Context, id int64, content model.JSONContent, editHistory model.JSONArray, editCount int32) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"content":      content,
			"edit_history": editHistory,
			"edit_count":   editCount,
			"status":       model.MessageStatusEdited,
		}).Error
}

func (r *MessageRepo) UpdateStatus(ctx context.Context, id int64, status int32) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", id).
		Update("status", status).Error
}

func (r *MessageRepo) Search(ctx context.Context, filter SearchFilter) ([]model.Message, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Message{})

	if filter.ConvID > 0 {
		q = q.Where("conv_id = ?", filter.ConvID)
	}
	if filter.Keyword != "" {
		q = q.Where("content::text ILIKE ?", "%"+filter.Keyword+"%")
	}
	if filter.SenderID > 0 {
		q = q.Where("sender_id = ?", filter.SenderID)
	}
	if filter.SenderType != "" {
		q = q.Where("sender_type = ?", filter.SenderType)
	}
	if len(filter.MessageTypes) > 0 {
		q = q.Where("msg_type IN ?", filter.MessageTypes)
	}
	if filter.StartTime > 0 {
		q = q.Where("created_at >= to_timestamp(?)", filter.StartTime)
	}
	if filter.EndTime > 0 {
		q = q.Where("created_at <= to_timestamp(?)", filter.EndTime)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var msgs []model.Message
	if err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&msgs).Error; err != nil {
		return nil, 0, err
	}
	return msgs, total, nil
}

func (r *MessageRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.Message{}, id).Error
}
