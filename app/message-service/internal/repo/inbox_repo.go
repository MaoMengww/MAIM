package repo

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm/clause"
)

type InboxRepo struct {
	db *database.DB
}

func NewInboxRepo(db *database.DB) *InboxRepo {
	return &InboxRepo{db: db}
}

func (r *InboxRepo) BatchInsert(ctx context.Context, inboxes []model.UserInbox) error {
	if len(inboxes) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(&inboxes).Error
}

func (r *InboxRepo) MarkDeleted(ctx context.Context, userID, convID, messageID int64) error {
	return r.db.WithContext(ctx).Model(&model.UserInbox{}).
		Where("user_id = ? AND conv_id = ? AND message_id = ?", userID, convID, messageID).
		Update("is_deleted", true).Error
}

func (r *InboxRepo) UpdateReadSeq(ctx context.Context, userID, convID, seq int64) error {
	return r.db.WithContext(ctx).Model(&model.UserInbox{}).
		Where("user_id = ? AND conv_id = ?", userID, convID).
		Update("last_read_seq", seq).Error
}

func (r *InboxRepo) GetByUserAndConv(ctx context.Context, userID, convID int64, fromSeq int64, limit int32) ([]model.UserInbox, error) {
	var inboxes []model.UserInbox
	q := r.db.WithContext(ctx).
		Where("user_id = ? AND conv_id = ? AND is_deleted = ?", userID, convID, false)

	if fromSeq > 0 {
		q = q.Where("seq > ?", fromSeq)
		err := q.Order("seq ASC").Limit(int(limit)).Find(&inboxes).Error
		if err != nil {
			return nil, err
		}
		return inboxes, nil
	}

	// 首次加载：取最新的 limit 条，翻转回升序
	err := q.Order("seq DESC").Limit(int(limit)).Find(&inboxes).Error
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(inboxes)-1; i < j; i, j = i+1, j-1 {
		inboxes[i], inboxes[j] = inboxes[j], inboxes[i]
	}
	return inboxes, nil
}

func (r *InboxRepo) GetMaxSeq(ctx context.Context, userID, convID int64) (int64, error) {
	var seq int64
	err := r.db.WithContext(ctx).
		Model(&model.UserInbox{}).
		Where("user_id = ? AND conv_id = ? AND is_deleted = ?", userID, convID, false).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&seq).Error
	return seq, err
}

func (r *InboxRepo) DeleteByUser(ctx context.Context, userID, convID, messageID int64) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND conv_id = ? AND message_id = ?", userID, convID, messageID).
		Delete(&model.UserInbox{}).Error
}
