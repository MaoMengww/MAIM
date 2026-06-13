package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"gorm.io/gorm"
)

type SearchFilter struct {
	UserID       int64
	ConvID       int64
	Keyword      string
	SenderID     int64
	SenderType   string
	MessageTypes []int32
	StartTime    int64
	EndTime      int64
	Page         int
	PageSize     int
}

type MessageRepoInterface interface {
	Insert(ctx context.Context, msg *model.Message) error
	GetByID(ctx context.Context, id int64) (*model.Message, error)
	GetByIDs(ctx context.Context, ids []int64) ([]model.Message, error)
	GetByConvID(ctx context.Context, convID int64, limit int, cursor int64, beforeTime, afterTime int64, filterTypes []int32) ([]model.Message, error)
	GetAroundSeq(ctx context.Context, convID int64, seq int64, limit int32) ([]model.Message, error)
	GetByConvIDAndSeq(ctx context.Context, convID int64, fromSeq int64, limit int32) ([]model.Message, error)
	GetMaxSeq(ctx context.Context, convID int64) (int64, error)
	UpdateContent(ctx context.Context, id int64, content model.JSONContent, editHistory model.JSONArray, editCount int32) error
	UpdateContentWithTx(ctx context.Context, tx *gorm.DB, id int64, content model.JSONContent, editHistory model.JSONArray, editCount int32) error
	UpdateStatus(ctx context.Context, id int64, status int32) error
	UpdateStatusWithTx(ctx context.Context, tx *gorm.DB, id int64, status int32) error
	Search(ctx context.Context, filter SearchFilter) ([]model.Message, int64, error)
	Delete(ctx context.Context, id int64) error
	DeleteWithTx(ctx context.Context, tx *gorm.DB, id int64) error
}

type InboxRepoInterface interface {
	BatchInsert(ctx context.Context, inboxes []model.UserInbox) error
	MarkDeleted(ctx context.Context, userID, convID, messageID int64) error
	UpdateReadSeq(ctx context.Context, userID, convID, seq int64) error
	GetByUserAndConv(ctx context.Context, userID, convID int64, fromSeq int64, limit int32) ([]model.UserInbox, error)
	GetMaxSeq(ctx context.Context, userID, convID int64) (int64, error)
	DeleteByUser(ctx context.Context, userID, convID, messageID int64) error
	ExistsByMessageID(ctx context.Context, messageID, convID int64) (bool, error)
}

type BroadcastRepoInterface interface {
	Insert(ctx context.Context, broadcast *model.Broadcast) error
	GetByID(ctx context.Context, id int64) (*model.Broadcast, error)
	List(ctx context.Context, scope string, page, pageSize int) ([]model.Broadcast, int64, error)
	ListByUser(ctx context.Context, userID int64, page, pageSize int) ([]model.Broadcast, int64, error)
}

type SequenceRepoInterface interface {
	GetCurrentSeq(ctx context.Context, convID int64) (int64, error)
	SetCurrentSeq(ctx context.Context, convID int64, seq int64) error
	NextSeq(ctx context.Context, db *gorm.DB, convID int64) (int64, error)
}

type OutboxRepoInterface interface {
	// Insert inserts an outbox event within an existing transaction.
	Insert(ctx context.Context, tx *gorm.DB, event *model.OutboxEvent) error
	// FetchPending returns pending events ordered by created_at ASC, using SKIP LOCKED.
	FetchPending(ctx context.Context, limit int) ([]model.OutboxEvent, error)
	MarkSent(ctx context.Context, id int64) error
	MarkRetry(ctx context.Context, id int64, nextRetryAt time.Time, lastError string) error
	MarkFailed(ctx context.Context, id int64, lastError string) error
	// DeleteSentBefore deletes sent events older than the given time.
	DeleteSentBefore(ctx context.Context, before time.Time, limit int) (int64, error)
	CountByStatus(ctx context.Context, status int16) (int64, error)
}
