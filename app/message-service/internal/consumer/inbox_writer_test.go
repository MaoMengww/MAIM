package consumer

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type testMemberResolver struct {
	members []int64
}

func (r testMemberResolver) GetConvMembers(context.Context, int64) ([]int64, error) {
	return r.members, nil
}

func TestInboxWriter_HandleMessageCreatedAcceptsNumericMessageID(t *testing.T) {
	db, mock := setupMockDB(t)
	writer := NewInboxWriter(
		repo.NewInboxRepo(db),
		testMemberResolver{members: []int64{10, 11}},
		logx.DefaultLogger(),
		0,
		nil,
	)

	// Idempotency check: ExistsByMessageID (should return 0 = not found)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "user_inbox"`).
		WithArgs(int64(333858130628710400), int64(333858130628710401)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO "user_inbox"`).
		WillReturnResult(sqlmock.NewResult(2, 2))
	mock.ExpectCommit()

	err := writer.handleMessageCreated(context.Background(), []byte(`{
		"message_id":333858130628710400,
		"conv_id":333858130628710401,
		"sender_id":10,
		"msg_type":1,
		"content":{"text":"hello"},
		"seq":3,
		"reply_to_msg_id":0,
		"created_at":1710000000
	}`))

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func setupMockDB(t *testing.T) (*database.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	return &database.DB{DB: gormDB}, mock
}
