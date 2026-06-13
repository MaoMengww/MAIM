package repo

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SequenceRepo struct {
	db *database.DB
}

func NewSequenceRepo(db *database.DB) *SequenceRepo {
	return &SequenceRepo{db: db}
}

func (r *SequenceRepo) GetCurrentSeq(ctx context.Context, convID int64) (int64, error) {
	var seq model.Sequence
	err := r.db.WithContext(ctx).Where("conv_id = ?", convID).First(&seq).Error
	if err != nil {
		if err.Error() == "record not found" {
			return 0, nil
		}
		return 0, err
	}
	return seq.CurrentSeq, nil
}

func (r *SequenceRepo) SetCurrentSeq(ctx context.Context, convID int64, seq int64) error {
	s := model.Sequence{
		ConvID:     convID,
		CurrentSeq: seq,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conv_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"current_seq"}),
	}).Create(&s).Error
}

// NextSeq atomically increments the conversation sequence using PostgreSQL's
// INSERT ... ON CONFLICT DO UPDATE ... RETURNING. This avoids race conditions
// without explicit FOR UPDATE locking — PG serializes the UPSERT internally.
// The `db` parameter should be the current transaction handle (*gorm.DB from
// Transaction callback) so the seq increment is atomic with the message insert.
func (r *SequenceRepo) NextSeq(ctx context.Context, db *gorm.DB, convID int64) (int64, error) {
	var nextSeq int64
	err := db.WithContext(ctx).Raw(`
		INSERT INTO msg.sequences (conv_id, current_seq)
		VALUES (?, 1)
		ON CONFLICT (conv_id) DO UPDATE SET current_seq = msg.sequences.current_seq + 1
		RETURNING current_seq
	`, convID).Scan(&nextSeq).Error
	return nextSeq, err
}
