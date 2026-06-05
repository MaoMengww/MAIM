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

func TestInboxRepo_BatchInsert(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	now := time.Now()
	inboxes := []model.UserInbox{
		{UserID: 1, ConvID: 100, MessageID: 1001, Seq: 1, CreatedAt: now},
		{UserID: 2, ConvID: 100, MessageID: 1001, Seq: 1, CreatedAt: now},
	}

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "user_inbox"`).
		WillReturnResult(sqlmock.NewResult(2, 2))
	mock.ExpectCommit()

	err := repo.BatchInsert(context.Background(), inboxes)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboxRepo_BatchInsert_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	err := repo.BatchInsert(context.Background(), []model.UserInbox{})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboxRepo_MarkDeleted(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "user_inbox" SET .+ WHERE user_id = \$2 AND conv_id = \$3 AND message_id = \$4`).
		WithArgs(sqlmock.AnyArg(), int64(1), int64(100), int64(1001)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.MarkDeleted(context.Background(), 1, 100, 1001)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboxRepo_UpdateReadSeq(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "user_inbox" SET .+ WHERE user_id = \$2 AND conv_id = \$3`).
		WithArgs(sqlmock.AnyArg(), int64(1), int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateReadSeq(context.Background(), 1, 100, 10)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboxRepo_GetByUserAndConv(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"user_id", "conv_id", "message_id", "seq", "last_read_seq", "is_deleted", "created_at"}).
		AddRow(1, 100, 1002, 2, 0, false, now).
		AddRow(1, 100, 1001, 1, 0, false, now)

	mock.ExpectQuery(`SELECT \* FROM "user_inbox" WHERE user_id = \$1 AND conv_id = \$2 AND is_deleted = \$3 ORDER BY seq DESC LIMIT \$4`).
		WithArgs(int64(1), int64(100), false, 50).
		WillReturnRows(rows)

	inboxes, err := repo.GetByUserAndConv(context.Background(), 1, 100, 0, 50)
	require.NoError(t, err)
	assert.Len(t, inboxes, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboxRepo_GetByUserAndConv_WithFromSeq(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{"user_id", "conv_id", "message_id", "seq", "last_read_seq", "is_deleted", "created_at"}).
		AddRow(1, 100, 1003, 3, 0, false, now)

	mock.ExpectQuery(`SELECT \* FROM "user_inbox" WHERE \(user_id = \$1 AND conv_id = \$2 AND is_deleted = \$3\) AND seq > \$4 ORDER BY seq ASC LIMIT \$5`).
		WithArgs(int64(1), int64(100), false, int64(2), 50).
		WillReturnRows(rows)

	inboxes, err := repo.GetByUserAndConv(context.Background(), 1, 100, 2, 50)
	require.NoError(t, err)
	assert.Len(t, inboxes, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboxRepo_GetMaxSeq(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(seq\), 0\) FROM "user_inbox" WHERE user_id = \$1 AND conv_id = \$2 AND is_deleted = \$3`).
		WithArgs(int64(1), int64(100), false).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(15))

	seq, err := repo.GetMaxSeq(context.Background(), 1, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(15), seq)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboxRepo_DeleteByUser(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewInboxRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "user_inbox" WHERE user_id = \$1 AND conv_id = \$2 AND message_id = \$3`).
		WithArgs(int64(1), int64(100), int64(1001)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.DeleteByUser(context.Background(), 1, 100, 1001)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
