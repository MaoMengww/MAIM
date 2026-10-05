package repo

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

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

// BatchInsert commits positions atomically; every mutation has its own ChangeID.
func (r *InboxRepo) BatchInsert(ctx context.Context, inboxes []model.UserInbox) error {
	if len(inboxes) == 0 {
		return nil
	}
	for _, entry := range inboxes {
		if entry.UserID <= 0 || entry.ConvID <= 0 || entry.ChangeID <= 0 {
			return fmt.Errorf("inbox entry requires a user, conversation and change")
		}
		switch entry.Kind {
		case model.InboxMessageNew, model.InboxMessageEdited, model.InboxMessageRecalled, model.InboxMessageDeleted:
			if entry.MessageID <= 0 {
				return fmt.Errorf("message change requires a message")
			}
		case model.InboxConversationUpsert, model.InboxConversationRemoved:
		case model.InboxReadUpdated:
			if entry.LastReadSeq < 0 {
				return fmt.Errorf("read sequence must be nonnegative")
			}
		default:
			return fmt.Errorf("unknown inbox kind: %s", entry.Kind)
		}
	}
	slices.SortStableFunc(inboxes, func(a, b model.UserInbox) int { return cmp.Compare(a.UserID, b.UserID) })
	streams := make([]model.InboxStream, 0, len(inboxes))
	userIDs := make([]int64, 0, len(inboxes))
	for _, entry := range inboxes {
		if len(userIDs) == 0 || userIDs[len(userIDs)-1] != entry.UserID {
			streams = append(streams, model.InboxStream{UserID: entry.UserID})
			userIDs = append(userIDs, entry.UserID)
		}
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&streams).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id IN ?", userIDs).
			Order("user_id").Find(&streams).Error; err != nil {
			return err
		}
		streamIndex := 0
		for _, entry := range inboxes {
			applied := model.InboxAppliedChange{UserID: entry.UserID, ChangeID: entry.ChangeID}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&applied)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			for streams[streamIndex].UserID != entry.UserID {
				streamIndex++
			}
			if entry.Kind == model.InboxReadUpdated {
				var old model.UserInbox
				err := tx.Where("user_id = ? AND conv_id = ? AND kind = ?", entry.UserID, entry.ConvID, entry.Kind).Take(&old).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err == nil {
					if old.LastReadSeq >= entry.LastReadSeq {
						continue
					}
					// Coalescing is not retention; removing the old position must not advance its boundary.
					if err := tx.Delete(&old).Error; err != nil {
						return err
					}
				}
			}
			streams[streamIndex].Position++
			entry.Position = streams[streamIndex].Position
			if err := tx.Create(&entry).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"position"})}).Create(&streams).Error
	})
}

func (r *InboxRepo) MarkDeleted(ctx context.Context, tx *gorm.DB, userID, convID, messageID int64) error {
	return tx.WithContext(ctx).Model(&model.UserInbox{}).
		Where("user_id = ? AND conv_id = ? AND message_id = ?", userID, convID, messageID).
		Update("is_deleted", true).Error
}

// EnsureStream reserves a positive checkpoint even before the first change.
// This runs before the read snapshot, not inside a read-only transaction.
func (r *InboxRepo) EnsureStream(ctx context.Context, userID int64) error {
	var stream model.InboxStream
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Take(&stream).Error; err == nil && stream.Position > 0 {
		return nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := r.db.WithContext(ctx).Exec(`INSERT INTO inbox_streams (user_id, position) VALUES (?, 1)
		ON CONFLICT (user_id) DO NOTHING`, userID).Error; err != nil {
		return err
	}
	// Existing positive streams need no lock or write. Only a legacy empty
	// allocator reserves a checkpoint; a writer that won the race keeps its end.
	return r.db.WithContext(ctx).Model(&model.InboxStream{}).
		Where("user_id = ? AND position = 0", userID).Update("position", 1).Error
}

type InboxPage struct {
	Entries       []model.UserInbox
	NextPosition  int64
	HasMore       bool
	RebuildReason string
}

// ReadPage must share a repeatable-read snapshot with message hydration and
// rebuild reads. Position is a committed boundary, never an allocation counter.
func (r *InboxRepo) ReadPage(ctx context.Context, userID, position int64, limit int, cutoff time.Time) (InboxPage, error) {
	var stream model.InboxStream
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&stream).Error; err != nil {
		return InboxPage{}, err
	}
	var expiredPosition int64
	if err := r.db.WithContext(ctx).Model(&model.UserInbox{}).
		Where("user_id = ? AND created_at < ?", userID, cutoff).
		Select("COALESCE(MAX(position), 0)").Scan(&expiredPosition).Error; err != nil {
		return InboxPage{}, err
	}
	page := InboxPage{NextPosition: stream.Position}
	switch {
	case position == 0:
		page.RebuildReason = "new_device"
	case position < 0 || position > stream.Position:
		page.RebuildReason = "unknown_position"
	case position < max(stream.RetainedPosition, expiredPosition):
		page.RebuildReason = "expired_position"
	}
	if page.RebuildReason != "" {
		return page, nil
	}
	if err := r.db.WithContext(ctx).Where("user_id = ? AND position > ? AND position <= ?", userID, position, stream.Position).
		Order("position ASC").Limit(limit + 1).Find(&page.Entries).Error; err != nil {
		return InboxPage{}, err
	}
	page.HasMore = len(page.Entries) > limit
	if page.HasMore {
		page.Entries = page.Entries[:limit]
		page.NextPosition = page.Entries[len(page.Entries)-1].Position
	}
	return page, nil
}

// Prune removes only a prefix and advances its durable lower boundary in the
// same transaction. The allocator lock also serializes collection with fanout.
func (r *InboxRepo) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	var users []int64
	if err := r.db.WithContext(ctx).Model(&model.UserInbox{}).Where("created_at < ?", cutoff).
		Distinct("user_id").Order("user_id").Pluck("user_id", &users).Error; err != nil {
		return 0, err
	}
	var removed int64
	for _, userID := range users {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var stream model.InboxStream
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&stream).Error; err != nil {
				return err
			}
			var boundary int64
			if err := tx.Model(&model.UserInbox{}).Where("user_id = ? AND created_at < ?", userID, cutoff).
				Select("COALESCE(MAX(position), 0)").Scan(&boundary).Error; err != nil {
				return err
			}
			if boundary == 0 {
				return nil
			}
			result := tx.Where("user_id = ? AND position <= ?", userID, boundary).Delete(&model.UserInbox{})
			if result.Error != nil {
				return result.Error
			}
			if err := tx.Model(&stream).Update("retained_position", max(stream.RetainedPosition, boundary)).Error; err != nil {
				return err
			}
			removed += result.RowsAffected
			return nil
		})
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}
