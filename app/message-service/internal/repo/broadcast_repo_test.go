package repo

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcastRepo_Insert(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewBroadcastRepo(db)

	b := &model.Broadcast{
		ID:        5001,
		SenderID:  10,
		Content:   model.JSONContent{"text": "system announcement"},
		Scope:     "all",
		CreatedAt: time.Now(),
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "broadcasts"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5001))
	mock.ExpectCommit()

	err := repo.Insert(context.Background(), b)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBroadcastRepo_GetByID(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewBroadcastRepo(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "sender_id", "content", "scope", "scope_target_id", "created_at"}).
		AddRow(1, 10, `{"text":"hello"}`, "all", 0, now)

	mock.ExpectQuery(`SELECT \* FROM "broadcasts" WHERE id = \$1 ORDER BY "broadcasts"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	b, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), b.ID)
	assert.Equal(t, "all", b.Scope)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBroadcastRepo_List(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewBroadcastRepo(db)

	now := time.Now()
	countRows := sqlmock.NewRows([]string{"count"}).AddRow(2)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "broadcasts"`).
		WillReturnRows(countRows)

	broadcastRows := sqlmock.NewRows([]string{"id", "sender_id", "content", "scope", "scope_target_id", "created_at"}).
		AddRow(1, 10, `{"text":"a"}`, "all", 0, now).
		AddRow(2, 11, `{"text":"b"}`, "all", 0, now)

	mock.ExpectQuery(`SELECT \* FROM "broadcasts" ORDER BY created_at DESC LIMIT \$1`).
		WithArgs(20).
		WillReturnRows(broadcastRows)

	broadcasts, total, err := repo.List(context.Background(), "", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, broadcasts, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBroadcastRepo_ListByUser(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewBroadcastRepo(db)

	now := time.Now()
	countRows := sqlmock.NewRows([]string{"count"}).AddRow(1)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "broadcasts" WHERE scope = \$1 OR \(scope = \$2 AND scope_target_id = \$3\)`).
		WithArgs("all", "user", int64(1)).
		WillReturnRows(countRows)

	broadcastRows := sqlmock.NewRows([]string{"id", "sender_id", "content", "scope", "scope_target_id", "created_at"}).
		AddRow(1, 10, `{"text":"a"}`, "all", 0, now)

	mock.ExpectQuery(`SELECT \* FROM "broadcasts" WHERE scope = \$1 OR \(scope = \$2 AND scope_target_id = \$3\) ORDER BY created_at DESC LIMIT \$4`).
		WithArgs("all", "user", int64(1), 20).
		WillReturnRows(broadcastRows)

	broadcasts, total, err := repo.ListByUser(context.Background(), 1, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, broadcasts, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}
