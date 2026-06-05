package repo

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
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
	UpdateStatus(ctx context.Context, id int64, status int32) error
	Search(ctx context.Context, filter SearchFilter) ([]model.Message, int64, error)
	Delete(ctx context.Context, id int64) error
}

type InboxRepoInterface interface {
	BatchInsert(ctx context.Context, inboxes []model.UserInbox) error
	MarkDeleted(ctx context.Context, userID, convID, messageID int64) error
	UpdateReadSeq(ctx context.Context, userID, convID, seq int64) error
	GetByUserAndConv(ctx context.Context, userID, convID int64, fromSeq int64, limit int32) ([]model.UserInbox, error)
	GetMaxSeq(ctx context.Context, userID, convID int64) (int64, error)
	DeleteByUser(ctx context.Context, userID, convID, messageID int64) error
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
}
