package repo

import (
	"context"

	"gorm.io/gorm"
)

type SequenceRepo struct{}

// NextSeq atomically increments the conversation sequence using PostgreSQL's
// INSERT ... ON CONFLICT DO UPDATE ... RETURNING. This avoids race conditions
// without explicit FOR UPDATE locking — PG serializes the UPSERT internally.
// The `db` parameter should be the current transaction handle (*gorm.DB from
// Transaction callback) so the seq increment is atomic with the message insert.
func (r *SequenceRepo) NextSeq(ctx context.Context, db *gorm.DB, convID int64) (int64, error) {
	var nextSeq int64
	err := db.WithContext(ctx).Raw(`
		INSERT INTO messaging.sequences (conv_id, current_seq)
		VALUES (?, 1)
		ON CONFLICT (conv_id) DO UPDATE SET current_seq = messaging.sequences.current_seq + 1
		RETURNING current_seq
	`, convID).Scan(&nextSeq).Error
	return nextSeq, err
}
