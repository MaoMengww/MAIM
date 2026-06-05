package repo

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/model"
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

func TestMessageRepo_Insert(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	msg := &model.Message{
		ID:        1001,
		ConvID:    100,
		SenderID:  10,
		Seq:       1,
		MsgType:   1,
		Content:   model.JSONContent{"text": "hello"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "messages"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1001))
	mock.ExpectCommit()

	err := repo.Insert(context.Background(), msg)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_GetByID(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	rows := sqlmock.NewRows([]string{"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type", "content", "reply_to_msg_id", "status", "edit_history", "edit_count", "created_at", "updated_at"}).
		AddRow(1, 100, 10, "", 1, 1, `{"text":"hello"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id = \$1 ORDER BY "messages"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	msg, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), msg.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_GetByIDs(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	rows := sqlmock.NewRows([]string{"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type", "content", "reply_to_msg_id", "status", "edit_history", "edit_count", "created_at", "updated_at"}).
		AddRow(1, 100, 10, "", 1, 1, `{"text":"a"}`, 0, 1, `[]`, 0, time.Now(), time.Now()).
		AddRow(2, 100, 11, "", 2, 1, `{"text":"b"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id IN \(\$1,\$2\)`).
		WithArgs(int64(1), int64(2)).
		WillReturnRows(rows)

	msgs, err := repo.GetByIDs(context.Background(), []int64{1, 2})
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_GetByIDs_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE id IN \(NULL\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type", "content", "reply_to_msg_id", "status", "edit_history", "edit_count", "created_at", "updated_at"}))

	msgs, err := repo.GetByIDs(context.Background(), []int64{})
	require.NoError(t, err)
	assert.Len(t, msgs, 0)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_UpdateStatus(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "messages" SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateStatus(context.Background(), 1, model.MessageStatusRecalled)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_UpdateContent(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "messages" SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateContent(context.Background(), 1,
		model.JSONContent{"text": "edited"},
		model.JSONArray{{"text": "original"}},
		2)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_Delete(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "messages" WHERE "messages"."id" = \$1`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), 1)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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

func TestMessageRepo_GetMaxSeq(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(seq\), 0\) FROM "messages" WHERE conv_id = \$1`).
		WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(42))

	seq, err := repo.GetMaxSeq(context.Background(), 100)
	require.NoError(t, err)
	assert.Equal(t, int64(42), seq)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_Search(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	countRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "messages" WHERE conv_id = \$1 AND content::text ILIKE \$2 AND sender_id = \$3 AND sender_type = \$4 AND msg_type IN \(\$5\) AND created_at >= to_timestamp\(\$6\) AND created_at <= to_timestamp\(\$7\)`).
		WithArgs(int64(100), "%hello%", int64(10), "user", int32(1), int64(1000), int64(2000)).
		WillReturnRows(countRows)

	msgRows := sqlmock.NewRows([]string{"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type", "content", "reply_to_msg_id", "status", "edit_history", "edit_count", "created_at", "updated_at"}).
		AddRow(1, 100, 10, "", 1, 1, `{"text":"hello"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE conv_id = \$1 AND content::text ILIKE \$2 AND sender_id = \$3 AND sender_type = \$4 AND msg_type IN \(\$5\) AND created_at >= to_timestamp\(\$6\) AND created_at <= to_timestamp\(\$7\) ORDER BY created_at DESC LIMIT \$8`).
		WithArgs(int64(100), "%hello%", int64(10), "user", int32(1), int64(1000), int64(2000), 20).
		WillReturnRows(msgRows)

	msgs, total, err := repo.Search(context.Background(), SearchFilter{
		ConvID:       100,
		Keyword:      "hello",
		SenderID:     10,
		SenderType:   "user",
		MessageTypes: []int32{1},
		StartTime:    1000,
		EndTime:      2000,
		Page:         1,
		PageSize:     20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, msgs, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_GetByConvIDAndSeq(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewMessageRepo(db)

	rows := sqlmock.NewRows([]string{"id", "conv_id", "sender_id", "client_msg_id", "seq", "msg_type", "content", "reply_to_msg_id", "status", "edit_history", "edit_count", "created_at", "updated_at"}).
		AddRow(3, 100, 10, "", 3, 1, `{"text":"new"}`, 0, 1, `[]`, 0, time.Now(), time.Now())

	mock.ExpectQuery(`SELECT \* FROM "messages" WHERE conv_id = \$1 AND seq > \$2 ORDER BY seq ASC LIMIT \$3`).
		WithArgs(int64(100), int64(1), 50).
		WillReturnRows(rows)

	msgs, err := repo.GetByConvIDAndSeq(context.Background(), 100, 1, 50)
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInterfaceCompliance(t *testing.T) {
	var _ MessageRepoInterface = (*MessageRepo)(nil)
	var _ InboxRepoInterface = (*InboxRepo)(nil)
	var _ BroadcastRepoInterface = (*BroadcastRepo)(nil)
	var _ SequenceRepoInterface = (*SequenceRepo)(nil)
}

func TestJSONContent_Value(t *testing.T) {
	c := model.JSONContent{"key": "val"}
	v, err := c.Value()
	require.NoError(t, err)
	assert.NotNil(t, v)
}

func TestJSONContent_Value_Nil(t *testing.T) {
	var c model.JSONContent
	v, err := c.Value()
	require.NoError(t, err)
	assert.NotNil(t, v)
}

func TestJSONArray_Value(t *testing.T) {
	a := model.JSONArray{{"a": 1}, {"b": 2}}
	v, err := a.Value()
	require.NoError(t, err)
	assert.NotNil(t, v)
}
