package repo

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSequenceRepo_GetCurrentSeq_Found(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewSequenceRepo(db)

	rows := sqlmock.NewRows([]string{"conv_id", "current_seq"}).
		AddRow(100, 42)

	mock.ExpectQuery(`SELECT \* FROM "sequences" WHERE conv_id = \$1 ORDER BY "sequences"."conv_id" LIMIT \$2`).
		WithArgs(int64(100), 1).
		WillReturnRows(rows)

	seq, err := repo.GetCurrentSeq(context.Background(), 100)
	require.NoError(t, err)
	assert.Equal(t, int64(42), seq)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSequenceRepo_GetCurrentSeq_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewSequenceRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "sequences" WHERE conv_id = \$1 ORDER BY "sequences"."conv_id" LIMIT \$2`).
		WithArgs(int64(999), 1).
		WillReturnRows(sqlmock.NewRows([]string{"conv_id", "current_seq"}))

	seq, err := repo.GetCurrentSeq(context.Background(), 999)
	require.NoError(t, err)
	assert.Equal(t, int64(0), seq)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSequenceRepo_SetCurrentSeq(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewSequenceRepo(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "sequences"`).
		WillReturnRows(sqlmock.NewRows([]string{"conv_id"}).AddRow(100))
	mock.ExpectCommit()

	err := repo.SetCurrentSeq(context.Background(), 100, 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSequenceModel_TableName(t *testing.T) {
	s := model.Sequence{}
	assert.Equal(t, "sequences", s.TableName())
}
