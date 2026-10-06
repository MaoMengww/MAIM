package repo

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupMockDB(t *testing.T) (*database.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	return &database.DB{DB: gormDB}, mock
}

func TestMessageRepo_GetAroundSeq(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	rows := sqlmock.NewRows([]string{"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type", "content", "reply_to_msg_id", "status", "edit_history", "edit_count", "created_at", "updated_at"}).
		AddRow(1, 100, 10, "", 8, 1, `{"text":"before"}`, 0, 1, `[]`, 0, time.Now(), time.Now()).
		AddRow(2, 100, 10, "", 10, 1, `{"text":"target"}`, 0, 1, `[]`, 0, time.Now(), time.Now()).
		AddRow(3, 100, 11, "", 12, 1, `{"text":"after"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE conv_id = \$1 AND seq BETWEEN \$2 AND \$3 ORDER BY seq ASC`).
		WithArgs(int64(100), int64(0), int64(20)).
		WillReturnRows(rows)

	msgs, err := repo.GetAroundSeq(context.Background(), 100, 10, 20)
	require.NoError(t, err)
	assert.Len(t, msgs, 3)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_GetByConvID_Pagination(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	rows := sqlmock.NewRows([]string{"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type", "content", "reply_to_msg_id", "status", "edit_history", "edit_count", "created_at", "updated_at"}).
		AddRow(3, 100, 10, "", 3, 1, `{"text":"c"}`, 0, 1, `[]`, 0, time.Now(), time.Now()).
		AddRow(2, 100, 10, "", 2, 1, `{"text":"b"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE conv_id = \$1 AND seq < \$2 ORDER BY seq DESC LIMIT \$3`).
		WithArgs(int64(100), int64(5), 10).
		WillReturnRows(rows)

	msgs, err := repo.GetByConvID(context.Background(), 100, 10, 5, 0, 0, nil)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}
