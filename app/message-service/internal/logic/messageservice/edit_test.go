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

func setupEditDB(t *testing.T) (*database.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return &database.DB{DB: gormDB}, mock
}

func TestEditMessage_NotSender(t *testing.T) {
	db, mock := setupEditDB(t)

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
		Config:      config.Config{Message: config.MessageConfig{EditWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
	}

	logic := NewEditMessageLogic(context.Background(), svcCtx)
	resp, err := logic.EditMessage(&message.EditMessageReq{
		MessageId: 1,
		UserId:    10, // different from sender_id=20
		Text:      &message.TextContent{Text: "edited"},
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only the sender can edit")
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEditMessage_EditWindowExpired(t *testing.T) {
	db, mock := setupEditDB(t)

	oldTime := time.Now().Add(-130 * time.Second) // older than 120s window
	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, model.MsgTypeText, `{"text":"hi"}`, 0, model.MessageStatusNormal, `[]`, 0, oldTime, oldTime)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	svcCtx := &svc.ServiceContext{
		Config:      config.Config{Message: config.MessageConfig{EditWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
	}

	logic := NewEditMessageLogic(context.Background(), svcCtx)
	resp, err := logic.EditMessage(&message.EditMessageReq{
		MessageId: 1,
		UserId:    10, // correct sender
		Text:      &message.TextContent{Text: "edited"},
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "edit window expired")
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEditMessage_NotTextType(t *testing.T) {
	db, mock := setupEditDB(t)

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type",
		"content", "reply_to_msg_id", "status", "edit_history", "edit_count",
		"created_at", "updated_at",
	}).AddRow(1, 100, 10, "", 1, model.MsgTypeImage, `{"url":"img"}`, 0, model.MessageStatusNormal, `[]`, 0, now, now)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	svcCtx := &svc.ServiceContext{
		Config:      config.Config{Message: config.MessageConfig{EditWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
	}

	logic := NewEditMessageLogic(context.Background(), svcCtx)
	resp, err := logic.EditMessage(&message.EditMessageReq{
		MessageId: 1,
		UserId:    10,
		Text:      &message.TextContent{Text: "edited"},
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only text messages can be edited")
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEditMessage_NotFound(t *testing.T) {
	db, mock := setupEditDB(t)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	svcCtx := &svc.ServiceContext{
		Config:      config.Config{Message: config.MessageConfig{EditWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
	}

	logic := NewEditMessageLogic(context.Background(), svcCtx)
	resp, err := logic.EditMessage(&message.EditMessageReq{
		MessageId: 999,
		UserId:    10,
		Text:      &message.TextContent{Text: "edit"},
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "message not found")
	assert.Nil(t, resp)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEditMessage_WithinWindow_Success(t *testing.T) {
	db, mock := setupEditDB(t)

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
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(1)).
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
		Config:      config.Config{Message: config.MessageConfig{EditWindowSeconds: 120}},
		DB:          db,
		MessageRepo: repo.NewMessageRepo(db),
		OutboxRepo:  repo.NewOutboxRepo(db),
		Snowflake:   sf,
	}

	logic := NewEditMessageLogic(context.Background(), svcCtx)
	resp, err := logic.EditMessage(&message.EditMessageReq{
		MessageId:      1,
		UserId:         10,
		ConversationId: 100,
		Text:           &message.TextContent{Text: "edited text", MentionUserIds: []int64{2}},
	})

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(0), resp.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
