package repo

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InboxRepo struct {
	db *database.DB
}

func NewInboxRepo(db *database.DB) *InboxRepo {
	return &InboxRepo{db: db}
}

// BatchInsert assigns positions and commits all recipients atomically. It
// compacts and sorts inboxes in place. A retry does not allocate a new position.
func (r *InboxRepo) BatchInsert(ctx context.Context, inboxes []model.UserInbox) error {
	if len(inboxes) == 0 {
		return nil
	}
	for i := range inboxes {
		if inboxes[i].UserID <= 0 || inboxes[i].ConvID <= 0 || inboxes[i].MessageID <= 0 {
			return fmt.Errorf("inbox entry requires a user, conversation and message")
		}
		inboxes[i].Kind = model.InboxMessageNew
	}
	// Lock every affected user in the same order, including first-time streams.
	// A later allocation cannot become visible ahead of an earlier transaction.
	slices.SortStableFunc(inboxes, func(a, b model.UserInbox) int { return cmp.Compare(a.UserID, b.UserID) })
	streams := make([]model.InboxStream, 0, len(inboxes))
	userIDs := make([]int64, 0, len(inboxes))
	keys := make([][]any, 0, len(inboxes))
	for _, entry := range inboxes {
		if len(userIDs) == 0 || userIDs[len(userIDs)-1] != entry.UserID {
			streams = append(streams, model.InboxStream{UserID: entry.UserID})
			userIDs = append(userIDs, entry.UserID)
		}
		keys = append(keys, []any{entry.UserID, entry.ConvID, entry.MessageID, entry.Kind})
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&streams).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id IN ?", userIDs).
			Order("user_id").Find(&streams).Error; err != nil {
			return err
		}
		type entryKey struct {
			userID, convID, messageID int64
			kind                      string
		}
		var existing []model.UserInbox
		if err := tx.Where("(user_id, conv_id, message_id, kind) IN ?", keys).Find(&existing).Error; err != nil {
			return err
		}
		seen := make(map[entryKey]struct{}, len(existing))
		for _, entry := range existing {
			seen[entryKey{entry.UserID, entry.ConvID, entry.MessageID, entry.Kind}] = struct{}{}
		}
		pending := inboxes[:0]
		streamIndex := 0
		for _, entry := range inboxes {
			key := entryKey{entry.UserID, entry.ConvID, entry.MessageID, entry.Kind}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			for streams[streamIndex].UserID != entry.UserID {
				streamIndex++
			}
			streams[streamIndex].Position++
			entry.Position = streams[streamIndex].Position
			pending = append(pending, entry)
		}
		if len(pending) == 0 {
			return nil
		}
		if err := tx.Create(&pending).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"position"}),
		}).Create(&streams).Error
	})
}

func (r *InboxRepo) MarkDeleted(ctx context.Context, tx *gorm.DB, userID, convID, messageID int64) error {
	return tx.WithContext(ctx).Model(&model.UserInbox{}).
		Where("user_id = ? AND conv_id = ? AND message_id = ?", userID, convID, messageID).
		Update("is_deleted", true).Error
}

// InboxMessageReference is the conversation read projection used by the current
// sync RPC. Seq comes from messages in the same query, never from inbox storage.
type InboxMessageReference struct {
	model.UserInbox
	Seq int64
}

func (r *InboxRepo) GetByUserAndConv(ctx context.Context, userID, convID int64, fromSeq int64, limit int32) ([]InboxMessageReference, error) {
	var inboxes []InboxMessageReference
	q := r.db.WithContext(ctx).Model(&model.UserInbox{}).Select("inbox_entries.*, messages.seq").
		Joins("JOIN messages ON messages.id = inbox_entries.message_id AND messages.conv_id = inbox_entries.conv_id").
		Where("inbox_entries.user_id = ? AND inbox_entries.conv_id = ? AND is_deleted = ? AND kind = ?", userID, convID, false, model.InboxMessageNew)

	if fromSeq > 0 {
		q = q.Where("messages.seq > ?", fromSeq)
		err := q.Order("messages.seq ASC").Limit(int(limit)).Find(&inboxes).Error
		if err != nil {
			return nil, err
		}
		return inboxes, nil
	}

	// 首次加载：取最新的 limit 条，翻转回升序
	err := q.Order("messages.seq DESC").Limit(int(limit)).Find(&inboxes).Error
	if err != nil {
		return nil, err
	}
	slices.Reverse(inboxes)
	return inboxes, nil
}

func (r *InboxRepo) GetMaxSeq(ctx context.Context, userID, convID int64) (int64, error) {
	var seq int64
	err := r.db.WithContext(ctx).
		Model(&model.UserInbox{}).
		Joins("JOIN messages ON messages.id = inbox_entries.message_id AND messages.conv_id = inbox_entries.conv_id").
		Where("inbox_entries.user_id = ? AND inbox_entries.conv_id = ? AND is_deleted = ? AND kind = ?", userID, convID, false, model.InboxMessageNew).
		Select("COALESCE(MAX(messages.seq), 0)").
		Scan(&seq).Error
	return seq, err
}
