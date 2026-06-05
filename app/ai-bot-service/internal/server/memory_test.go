package server

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/ai-bot-service/internal/svc"
	"github.com/maomeng/aim/app/ai-bot-service/pb/aibot"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupMockDB(t *testing.T) (*database.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		SkipDefaultTransaction: true,
	})
	require.NoError(t, err)
	return &database.DB{DB: gormDB}, mock
}

func newTestServer(db *database.DB) *AiBotServiceServer {
	return &AiBotServiceServer{
		svcCtx: &svc.ServiceContext{
			DB:     db,
			Logger: logx.DefaultLogger(),
		},
	}
}

// ---------------------------------------------------------------------------
// GetUserMemories
// ---------------------------------------------------------------------------

func TestGetUserMemories_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	// MAX(access_count) — Scan, error is ignored by FindByUser
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(access_count\), 0\).+FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(5))

	// Find all items
	rows := sqlmock.NewRows([]string{
		"id", "bot_id", "user_id", "memory_type", "content", "category",
		"subject", "predicate", "object",
		"importance", "confidence", "access_count",
		"last_accessed_at", "created_at", "updated_at",
	}).
		AddRow(1, 100, 200, "fact", "likes basketball", "hobby",
			"", "", "",
			0.8, 0.9, 3,
			nil, now, now).
		AddRow(2, 100, 200, "fact", "works in Beijing", "career",
			"", "", "",
			0.5, 0.8, 1,
			nil, now, now)

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnRows(rows)

	req := &aibot.GetUserMemoriesReq{
		BotId:  100,
		UserId: 200,
		Limit:  10,
	}
	rsp, err := srv.GetUserMemories(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, rsp)
	require.Len(t, rsp.Items, 2)

	assert.Equal(t, int64(1), rsp.Items[0].Id)
	assert.Equal(t, "likes basketball", rsp.Items[0].Content)
	assert.Equal(t, "fact", rsp.Items[0].MemoryType)
	assert.Equal(t, "hobby", rsp.Items[0].Category)
	assert.Equal(t, 0.8, rsp.Items[0].Importance)
	assert.Equal(t, now.Format("2006-01-02T15:04:05Z"), rsp.Items[0].CreatedAt)
	// FinalScore should have been computed by addFinalScores
	assert.Greater(t, rsp.Items[0].FinalScore, 0.0)

	assert.Equal(t, int64(2), rsp.Items[1].Id)
	assert.Equal(t, "works in Beijing", rsp.Items[1].Content)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserMemories_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(access_count\), 0\).+FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	req := &aibot.GetUserMemoriesReq{
		BotId:  100,
		UserId: 200,
		Limit:  10,
	}
	rsp, err := srv.GetUserMemories(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, rsp)
	assert.Empty(t, rsp.Items)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserMemories_DefaultLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(access_count\), 0\).+FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	req := &aibot.GetUserMemoriesReq{
		BotId:  100,
		UserId: 200,
		Limit:  0, // triggers the default limit of 20 in server
	}
	rsp, err := srv.GetUserMemories(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, rsp)
	assert.Empty(t, rsp.Items)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserMemories_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(access_count\), 0\).+FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnError(gorm.ErrInvalidDB)

	req := &aibot.GetUserMemoriesReq{
		BotId:  100,
		UserId: 200,
		Limit:  10,
	}
	_, err := srv.GetUserMemories(ctx, req)
	require.Error(t, err)

	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok)
	assert.Equal(t, errors.CodeDBError, bizErr.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// ForgetMemory
// ---------------------------------------------------------------------------

func TestForgetMemory_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	// FindByID returns a memory owned by bot 100, user 200
	rows := sqlmock.NewRows([]string{
		"id", "bot_id", "user_id", "memory_type", "content",
	}).
		AddRow(1, 100, 200, "fact", "some memory")

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE id = \$1.+ORDER BY "bot_memories"\."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	// Delete succeeds
	mock.ExpectExec(`DELETE FROM "bot_memories" WHERE id = \$1`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := &aibot.ForgetMemoryReq{
		BotId:    100,
		UserId:   200,
		MemoryId: 1,
	}
	_, err := srv.ForgetMemory(ctx, req)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestForgetMemory_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE id = \$1.+ORDER BY "bot_memories"\."id" LIMIT \$2`).
		WithArgs(int64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	req := &aibot.ForgetMemoryReq{
		BotId:    100,
		UserId:   200,
		MemoryId: 999,
	}
	_, err := srv.ForgetMemory(ctx, req)
	require.Error(t, err)

	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok)
	assert.Equal(t, errors.CodeNotFound, bizErr.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestForgetMemory_OwnershipMismatch_BotID(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"id", "bot_id", "user_id", "memory_type", "content",
	}).
		AddRow(1, 999, 200, "fact", "wrong bot memory")

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE id = \$1.+ORDER BY "bot_memories"\."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	req := &aibot.ForgetMemoryReq{
		BotId:    100,
		UserId:   200,
		MemoryId: 1,
	}
	_, err := srv.ForgetMemory(ctx, req)
	require.Error(t, err)

	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok)
	assert.Equal(t, errors.CodeForbidden, bizErr.Code)
	// No DELETE should have been issued
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestForgetMemory_OwnershipMismatch_UserID(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"id", "bot_id", "user_id", "memory_type", "content",
	}).
		AddRow(1, 100, 999, "fact", "wrong user memory")

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE id = \$1.+ORDER BY "bot_memories"\."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	req := &aibot.ForgetMemoryReq{
		BotId:    100,
		UserId:   200,
		MemoryId: 1,
	}
	_, err := srv.ForgetMemory(ctx, req)
	require.Error(t, err)

	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok)
	assert.Equal(t, errors.CodeForbidden, bizErr.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestForgetMemory_DeleteError(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	rows := sqlmock.NewRows([]string{
		"id", "bot_id", "user_id", "memory_type", "content",
	}).
		AddRow(1, 100, 200, "fact", "some memory")

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE id = \$1.+ORDER BY "bot_memories"\."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	mock.ExpectExec(`DELETE FROM "bot_memories" WHERE id = \$1`).
		WithArgs(int64(1)).
		WillReturnError(gorm.ErrInvalidDB)

	req := &aibot.ForgetMemoryReq{
		BotId:    100,
		UserId:   200,
		MemoryId: 1,
	}
	_, err := srv.ForgetMemory(ctx, req)
	require.Error(t, err)

	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok)
	assert.Equal(t, errors.CodeDBError, bizErr.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// ClearUserMemories
// ---------------------------------------------------------------------------

func TestClearUserMemories_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	mock.ExpectExec(`DELETE FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 5))

	req := &aibot.ClearUserMemoriesReq{
		BotId:  100,
		UserId: 200,
	}
	_, err := srv.ClearUserMemories(ctx, req)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestClearUserMemories_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	srv := newTestServer(db)
	ctx := context.Background()

	mock.ExpectExec(`DELETE FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(100), int64(200)).
		WillReturnError(gorm.ErrInvalidDB)

	req := &aibot.ClearUserMemoriesReq{
		BotId:  100,
		UserId: 200,
	}
	_, err := srv.ClearUserMemories(ctx, req)
	require.Error(t, err)

	bizErr, ok := errors.IsBizError(err)
	require.True(t, ok)
	assert.Equal(t, errors.CodeDBError, bizErr.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
