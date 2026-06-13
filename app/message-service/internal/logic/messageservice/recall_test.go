package messageservicelogic

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/config"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupRecallDB(t *testing.T) (*database.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return &database.DB{DB: gormDB}, mock
}

func TestRecallMessage_NotSender(t *testing.T) {
	db, mock := setupRecallDB(t)

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 20, "", 1, model.MsgTypeText, `{"text":"hi"}`, 0, model.MessageStatusNormal, `[]`, 0, now, now)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	svcCtx := &svc.ServiceContext{
		Config:      config.Config{Message: config.MessageConfig{RecallWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
	}

	logic := NewRecallMessageLogic(context.Background(), svcCtx)
	resp, err := logic.RecallMessage(&message.RecallMessageReq{
		MessageId: 1,
		UserId:    10, // different from sender_id=20
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only the sender can recall")
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRecallMessage_RecallWindowExpired(t *testing.T) {
	db, mock := setupRecallDB(t)

	oldTime := time.Now().Add(-130 * time.Second) // older than 120s
	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, model.MsgTypeText, `{"text":"hi"}`, 0, model.MessageStatusNormal, `[]`, 0, oldTime, oldTime)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	svcCtx := &svc.ServiceContext{
		Config:      config.Config{Message: config.MessageConfig{RecallWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
	}

	logic := NewRecallMessageLogic(context.Background(), svcCtx)
	resp, err := logic.RecallMessage(&message.RecallMessageReq{
		MessageId: 1,
		UserId:    10, // correct sender
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "recall window expired")
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRecallMessage_NotFound(t *testing.T) {
	db, mock := setupRecallDB(t)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	svcCtx := &svc.ServiceContext{
		Config:      config.Config{Message: config.MessageConfig{RecallWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
	}

	logic := NewRecallMessageLogic(context.Background(), svcCtx)
	resp, err := logic.RecallMessage(&message.RecallMessageReq{
		MessageId: 999,
		UserId:    10,
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "message not found")
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRecallMessage_WithinWindow_Success(t *testing.T) {
	db, mock := setupRecallDB(t)

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, model.MsgTypeText, `{"text":"hi"}`, 0, model.MessageStatusNormal, `[]`, 0, now, now)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "messages" SET`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(1)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// Outbox INSERT inside the same transaction (GORM generates 11-arg INSERT)
	mock.ExpectQuery(`INSERT INTO "msg"."outbox_events"`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectCommit()

	sf, err := snowflake.NewNode(1)
	require.NoError(t, err)

	svcCtx := &svc.ServiceContext{
		Config:      config.Config{Message: config.MessageConfig{RecallWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
		OutboxRepo:  repo.NewOutboxRepo(db),
		Snowflake:   sf,
	}

	logic := NewRecallMessageLogic(context.Background(), svcCtx)
	resp, err := logic.RecallMessage(&message.RecallMessageReq{
		MessageId:      1,
		UserId:         10,
		ConversationId: 100,
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(0), resp.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
