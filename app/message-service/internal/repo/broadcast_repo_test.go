package repo

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/stretchr/testify/assert"
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
