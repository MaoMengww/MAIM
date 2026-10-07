package repo

import (
	"context"
	"fmt"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"

	"gorm.io/gorm"
)

type SequenceRepo struct{}

// NextSeq atomically increments the conversation sequence using PostgreSQL's
// INSERT ... ON CONFLICT DO UPDATE ... RETURNING. This avoids race conditions
// without explicit FOR UPDATE locking — PG serializes the UPSERT internally.
// The `db` parameter should be the current transaction handle (*gorm.DB from
// Transaction callback) so the seq increment is atomic with the message insert.
func (r *SequenceRepo) NextSeq(ctx context.Context, db *gorm.DB, convID string) (int64, error) {
	if err := identity.Validate(convID); err != nil {
		return 0, err
	}
	var nextSeq int64
	result := db.WithContext(ctx).Raw(`
		INSERT INTO messaging.sequences (conv_id, current_seq)
		VALUES (?, 1)
		ON CONFLICT (conv_id) DO UPDATE SET current_seq = messaging.sequences.current_seq + 1
		WHERE messaging.sequences.current_seq < ?
		RETURNING current_seq
	`, convID, sequence.Max).Scan(&nextSeq)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, fmt.Errorf("conversation sequence exhausted")
	}
	return nextSeq, nil
}
