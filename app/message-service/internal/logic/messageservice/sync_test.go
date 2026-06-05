package messageservicelogic

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncMessages_LimitDefault(t *testing.T) {
	tctx := newTestSvcCtx(t)

	now := time.Now()
	inboxRows := sqlmock.NewRows([]string{
		"user_id", "conv_id", "message_id", "seq", "is_deleted", "created_at",
	}).AddRow(int64(10), int64(100), int64(1), int64(1), false, now)

	tctx.DBMock.ExpectQuery(`SELECT \* FROM "user_inbox" WHERE user_id = \$1 AND conv_id = \$2 AND is_deleted = \$3 ORDER BY seq DESC LIMIT \$4`).
		WithArgs(int64(10), int64(100), false, 50).
		WillReturnRows(inboxRows)

	msgRows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, model.MsgTypeText, `{"text":"a"}`, 0, model.MessageStatusNormal, `[]`, 0, now, now)

	tctx.DBMock.ExpectQuery(`SELECT \* FROM "messages" WHERE id IN \(\$1\)`).
		WithArgs(int64(1)).
		WillReturnRows(msgRows)

	logic := NewSyncMessagesLogic(context.Background(), tctx.SvcCtx)
	resp, err := logic.SyncMessages(&message.SyncMessagesReq{
		UserId:         10,
		ConversationId: 100,
		FromSeq:        0,
		Limit:          0,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Messages, 1)
	assert.False(t, resp.HasMore)
	assert.Equal(t, int64(1), resp.MaxSeq)
	assert.NoError(t, tctx.DBMock.ExpectationsWereMet())
}

func TestSyncMessages_HasMore(t *testing.T) {
	tctx := newTestSvcCtx(t)

	now := time.Now()
	inboxRows := sqlmock.NewRows([]string{
		"user_id", "conv_id", "message_id", "seq", "is_deleted", "created_at",
	}).AddRow(int64(10), int64(100), int64(2), int64(2), false, now).
		AddRow(int64(10), int64(100), int64(1), int64(1), false, now)

	tctx.DBMock.ExpectQuery(`SELECT \* FROM "user_inbox" WHERE user_id = \$1 AND conv_id = \$2 AND is_deleted = \$3 ORDER BY seq DESC LIMIT \$4`).
		WithArgs(int64(10), int64(100), false, 2).
		WillReturnRows(inboxRows)

	msgRows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, model.MsgTypeText, `{"text":"a"}`, 0, model.MessageStatusNormal, `[]`, 0, now, now).
		AddRow(2, 100, 10, "", 2, model.MsgTypeText, `{"text":"b"}`, 0, model.MessageStatusNormal, `[]`, 0, now, now)

	tctx.DBMock.ExpectQuery(`SELECT \* FROM "messages" WHERE id IN \(\$1,\$2\)`).
		WithArgs(int64(1), int64(2)).
		WillReturnRows(msgRows)

	logic := NewSyncMessagesLogic(context.Background(), tctx.SvcCtx)
	resp, err := logic.SyncMessages(&message.SyncMessagesReq{
		UserId:         10,
		ConversationId: 100,
		FromSeq:        0,
		Limit:          2,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Messages, 2)
	assert.False(t, resp.HasMore)
	assert.Equal(t, int64(2), resp.MaxSeq)
	assert.NoError(t, tctx.DBMock.ExpectationsWereMet())
}

func TestSyncMessages_LimitClamped(t *testing.T) {
	tctx := newTestSvcCtx(t)

	now := time.Now()
	inboxRows := sqlmock.NewRows([]string{
		"user_id", "conv_id", "message_id", "seq", "is_deleted", "created_at",
	}).AddRow(int64(10), int64(100), int64(1), int64(6), false, now)

	tctx.DBMock.ExpectQuery(`SELECT \* FROM "user_inbox" WHERE \(user_id = \$1 AND conv_id = \$2 AND is_deleted = \$3\) AND seq > \$4 ORDER BY seq ASC LIMIT \$5`).
		WithArgs(int64(10), int64(100), false, int64(5), 100).
		WillReturnRows(inboxRows)

	msgRows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 6, model.MsgTypeText, `{"text":"a"}`, 0, model.MessageStatusNormal, `[]`, 0, now, now)

	tctx.DBMock.ExpectQuery(`SELECT \* FROM "messages" WHERE id IN \(\$1\)`).
		WithArgs(int64(1)).
		WillReturnRows(msgRows)

	logic := NewSyncMessagesLogic(context.Background(), tctx.SvcCtx)
	resp, err := logic.SyncMessages(&message.SyncMessagesReq{
		UserId:         10,
		ConversationId: 100,
		FromSeq:        5,
		Limit:          200,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Messages, 1)
	assert.NoError(t, tctx.DBMock.ExpectationsWereMet())
}
