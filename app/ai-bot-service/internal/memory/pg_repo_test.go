package memory

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

func setupMemDB(t *testing.T) (*database.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return &database.DB{DB: gormDB}, mock
}

func TestPgRepo_Insert(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	now := time.Now()
	item := &MemoryItem{
		BotID:      1,
		UserID:     100,
		MemoryType: "fact",
		Content:    "喜欢打篮球",
		Category:   "爱好",
		Importance: 0.8,
		Confidence: 0.9,
		CreatedAt:  now,
	}

	// findSimilar: load all facts for user+bot (none found)
	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE`).
		WithArgs(int64(1), int64(100), "fact").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	// Insert (GORM wraps in transaction)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "bot_memories"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	err := repo.Insert(context.Background(), item)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_Insert_Dedup(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	item := &MemoryItem{
		BotID:      1,
		UserID:     100,
		MemoryType: "fact",
		Content:    "喜欢打篮球",
		Category:   "爱好",
		Importance: 0.9,
		Confidence: 0.95,
	}

	// findSimilar: find existing with similar content
	rows := sqlmock.NewRows([]string{"id", "bot_id", "user_id", "memory_type", "content", "importance", "confidence", "created_at", "updated_at"}).
		AddRow(1, 1, 100, "fact", "喜欢打篮球和足球", 0.5, 0.7, time.Now(), time.Now())
	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE`).
		WithArgs(int64(1), int64(100), "fact").
		WillReturnRows(rows)

	// Save in mergeFact: BEGIN → UPDATE → COMMIT (no SELECT, ID is non-zero)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "bot_memories" SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Insert(context.Background(), item)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_FindByUser(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	now := time.Now()

	// MAX(access_count)
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(access_count\), 0\)`).
		WithArgs(int64(1), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(3))

	rows := sqlmock.NewRows([]string{
		"id", "bot_id", "user_id", "memory_type", "content", "category",
		"importance", "confidence", "access_count", "last_accessed_at", "created_at",
	}).
		AddRow(1, 1, 100, "fact", "喜欢打篮球", "爱好", 0.8, 0.9, 3, now, now).
		AddRow(2, 1, 100, "fact", "在北京工作", "职业", 0.5, 0.8, 1, now, now)

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(1), int64(100)).
		WillReturnRows(rows)

	items, err := repo.FindByUser(context.Background(), 1, 100, 10)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.GreaterOrEqual(t, items[0].FinalScore, items[1].FinalScore)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_FindByUser_DefaultLimit(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	mock.ExpectQuery(`SELECT COALESCE\(MAX\(access_count\), 0\)`).
		WithArgs(int64(1), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))

	mock.ExpectQuery(`SELECT \* FROM "bot_memories"`).
		WithArgs(int64(1), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	items, err := repo.FindByUser(context.Background(), 1, 100, 0)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_CountByUser(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "bot_memories" WHERE`).
		WithArgs(int64(1), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))

	count, err := repo.CountByUser(context.Background(), 1, 100)
	require.NoError(t, err)
	assert.Equal(t, int64(42), count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_Delete(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "bot_memories" WHERE id = \$1`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), 1)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_DeleteByUser(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "bot_memories" WHERE bot_id = \$1 AND user_id = \$2`).
		WithArgs(int64(1), int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()

	err := repo.DeleteByUser(context.Background(), 1, 100)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_FindByID(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "bot_id", "user_id", "memory_type", "content",
		"importance", "access_count", "last_accessed_at", "created_at",
	}).AddRow(1, 1, 100, "fact", "test", 0.5, 1, now, now)

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE id = \$1`).
		WithArgs(int64(1), 1).
		WillReturnRows(rows)

	item, err := repo.FindByID(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, "test", item.Content)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_FindByID_NotFound(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "bot_memories" WHERE id = \$1`).
		WithArgs(int64(999), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	_, err := repo.FindByID(context.Background(), 999)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepo_Update(t *testing.T) {
	db, mock := setupMemDB(t)
	repo := NewPgRepo(db)

	now := time.Now()
	item := &MemoryItem{
		ID:             1,
		Content:        "updated content",
		Importance:     0.9,
		AccessCount:    5,
		LastAccessedAt: &now,
	}

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "bot_memories" SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Update(context.Background(), item)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
