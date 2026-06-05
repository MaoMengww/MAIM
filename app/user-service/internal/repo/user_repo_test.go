package repo

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/user-service/internal/model"
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

var userColumns = []string{
	"id", "username", "password_hash", "phone", "email", "avatar",
	"gender", "bio", "birthday", "balance", "settings",
	"created_at", "updated_at",
}

func userRow(id int64, username, phone, email string) []driverValue {
	now := time.Now()
	return []driverValue{
		id, username, "hash", phone, email, "",
		int32(0), "bio", int64(0), float64(0), []byte(`{}`),
		now, now,
	}
}

// driverValue is an alias for driver.Value to keep mock row creation concise.
type driverValue = driver.Value

func TestNewUserRepo(t *testing.T) {
	repo := NewUserRepo(nil)
	assert.NotNil(t, repo)
}

// ---------- Create ----------

func TestUserRepo_Create(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	user := &model.User{
		ID:       1001,
		Username: "testuser",
		Bio:      "hello",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1001))
	mock.ExpectCommit()

	err := repo.Create(context.Background(), user)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_Create_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	user := &model.User{Username: "failme"}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "users"`).
		WillReturnError(errors.New("duplicate key"))
	mock.ExpectRollback()

	err := repo.Create(context.Background(), user)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate key")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- GetByID ----------

func TestUserRepo_GetByID(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE id = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(userRow(1, "alice", "13800000001", "alice@test.com")...))

	user, err := repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), user.ID)
	assert.Equal(t, "alice", user.Username)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByID_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE id = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs(int64(999), 1).
		WillReturnRows(sqlmock.NewRows(userColumns))

	user, err := repo.GetByID(context.Background(), 999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByID_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE id = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnError(errors.New("connection refused"))

	user, err := repo.GetByID(context.Background(), 1)
	assert.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "connection refused")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- GetByUsername ----------

func TestUserRepo_GetByUsername(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE username = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("bob", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(userRow(2, "bob", "13800000002", "bob@test.com")...))

	user, err := repo.GetByUsername(context.Background(), "bob")
	require.NoError(t, err)
	assert.Equal(t, int64(2), user.ID)
	assert.Equal(t, "bob", user.Username)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByUsername_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE username = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("nobody", 1).
		WillReturnRows(sqlmock.NewRows(userColumns))

	user, err := repo.GetByUsername(context.Background(), "nobody")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByUsername_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE username = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("error", 1).
		WillReturnError(errors.New("timeout"))

	user, err := repo.GetByUsername(context.Background(), "error")
	assert.Error(t, err)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- GetByPhone ----------

func TestUserRepo_GetByPhone(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE phone = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("13800000003", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(userRow(3, "carol", "13800000003", "carol@test.com")...))

	user, err := repo.GetByPhone(context.Background(), "13800000003")
	require.NoError(t, err)
	assert.Equal(t, int64(3), user.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByPhone_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE phone = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("00000000000", 1).
		WillReturnRows(sqlmock.NewRows(userColumns))

	user, err := repo.GetByPhone(context.Background(), "00000000000")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByPhone_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE phone = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("00000000000", 1).
		WillReturnError(errors.New("db error"))

	user, err := repo.GetByPhone(context.Background(), "00000000000")
	assert.Error(t, err)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- GetByEmail ----------

func TestUserRepo_GetByEmail(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE email = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("dave@test.com", 1).
		WillReturnRows(sqlmock.NewRows(userColumns).AddRow(userRow(4, "dave", "13800000004", "dave@test.com")...))

	user, err := repo.GetByEmail(context.Background(), "dave@test.com")
	require.NoError(t, err)
	assert.Equal(t, int64(4), user.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByEmail_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE email = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("missing@test.com", 1).
		WillReturnRows(sqlmock.NewRows(userColumns))

	user, err := repo.GetByEmail(context.Background(), "missing@test.com")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetByEmail_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE email = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs("fail@test.com", 1).
		WillReturnError(errors.New("disk full"))

	user, err := repo.GetByEmail(context.Background(), "fail@test.com")
	assert.Error(t, err)
	assert.Nil(t, user)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- Update ----------

func TestUserRepo_Update(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "users" SET`).
		WithArgs("new bio", sqlmock.AnyArg(), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Update(context.Background(), 1, map[string]any{"bio": "new bio"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_Update_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "users" SET`).
		WithArgs("bio", sqlmock.AnyArg(), int64(1)).
		WillReturnError(errors.New("constraint violation"))
	mock.ExpectRollback()

	err := repo.Update(context.Background(), 1, map[string]any{"bio": "bio"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "constraint violation")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- BatchGetByIDs ----------

func TestUserRepo_BatchGetByIDs(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE id IN \(\$1,\$2\)`).
		WithArgs(int64(1), int64(2)).
		WillReturnRows(sqlmock.NewRows(userColumns).
			AddRow(userRow(1, "alice", "13800000001", "alice@test.com")...).
			AddRow(userRow(2, "bob", "13800000002", "bob@test.com")...))

	users, err := repo.BatchGetByIDs(context.Background(), []int64{1, 2})
	require.NoError(t, err)
	assert.Len(t, users, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_BatchGetByIDs_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	users, err := repo.BatchGetByIDs(context.Background(), []int64{})
	require.NoError(t, err)
	assert.Nil(t, users)
	// No DB call expected for empty slice
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_BatchGetByIDs_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE id IN \(\$1\)`).
		WithArgs(int64(1)).
		WillReturnError(errors.New("connection lost"))

	users, err := repo.BatchGetByIDs(context.Background(), []int64{1})
	assert.Error(t, err)
	assert.Nil(t, users)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- Search ----------

func TestUserRepo_Search(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	countRows := sqlmock.NewRows([]string{"count"}).AddRow(2)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username LIKE \$1`).
		WithArgs("%alice%").
		WillReturnRows(countRows)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE username LIKE \$1 ORDER BY id ASC LIMIT \$2`).
		WithArgs("%alice%", 20).
		WillReturnRows(sqlmock.NewRows(userColumns).
			AddRow(userRow(1, "alice", "13800000001", "alice@test.com")...))

	users, total, err := repo.Search(context.Background(), "alice", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, users, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_Search_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	countRows := sqlmock.NewRows([]string{"count"}).AddRow(0)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username LIKE \$1`).
		WithArgs("%nonexistent%").
		WillReturnRows(countRows)

	dataRows := sqlmock.NewRows(userColumns)
	mock.ExpectQuery(`SELECT \* FROM "users" WHERE username LIKE \$1 ORDER BY id ASC LIMIT \$2`).
		WithArgs("%nonexistent%", 20).
		WillReturnRows(dataRows)

	users, total, err := repo.Search(context.Background(), "nonexistent", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, users)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_Search_CountError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username LIKE \$1`).
		WithArgs("%error%").
		WillReturnError(errors.New("count failed"))

	users, total, err := repo.Search(context.Background(), "error", 1, 20)
	assert.Error(t, err)
	assert.Equal(t, int64(0), total)
	assert.Nil(t, users)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_Search_FindError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	countRows := sqlmock.NewRows([]string{"count"}).AddRow(5)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username LIKE \$1`).
		WithArgs("%fail%").
		WillReturnRows(countRows)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE username LIKE \$1 ORDER BY id ASC LIMIT \$2`).
		WithArgs("%fail%", 20).
		WillReturnError(errors.New("find failed"))

	users, total, err := repo.Search(context.Background(), "fail", 1, 20)
	assert.Error(t, err)
	assert.Equal(t, int64(0), total)
	assert.Nil(t, users)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_Search_DefaultPagination(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	countRows := sqlmock.NewRows([]string{"count"}).AddRow(0)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username LIKE \$1`).
		WithArgs("%test%").
		WillReturnRows(countRows)

	mock.ExpectQuery(`SELECT \* FROM "users" WHERE username LIKE \$1 ORDER BY id ASC LIMIT \$2`).
		WithArgs("%test%", 20).
		WillReturnRows(sqlmock.NewRows(userColumns))

	users, total, err := repo.Search(context.Background(), "test", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, users)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- ExistsByUsername ----------

func TestUserRepo_ExistsByUsername_True(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username = \$1`).
		WithArgs("existing").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	exists, err := repo.ExistsByUsername(context.Background(), "existing")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ExistsByUsername_False(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username = \$1`).
		WithArgs("nonexistent").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	exists, err := repo.ExistsByUsername(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ExistsByUsername_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE username = \$1`).
		WithArgs("error").
		WillReturnError(errors.New("query error"))

	_, err := repo.ExistsByUsername(context.Background(), "error")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- ExistsByPhone ----------

func TestUserRepo_ExistsByPhone_True(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE phone = \$1`).
		WithArgs("13800000001").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	exists, err := repo.ExistsByPhone(context.Background(), "13800000001")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ExistsByPhone_False(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE phone = \$1`).
		WithArgs("00000000000").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	exists, err := repo.ExistsByPhone(context.Background(), "00000000000")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ExistsByPhone_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE phone = \$1`).
		WithArgs("00000000000").
		WillReturnError(errors.New("phone error"))

	_, err := repo.ExistsByPhone(context.Background(), "00000000000")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- ExistsByEmail ----------

func TestUserRepo_ExistsByEmail_True(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE email = \$1`).
		WithArgs("alice@test.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	exists, err := repo.ExistsByEmail(context.Background(), "alice@test.com")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ExistsByEmail_False(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE email = \$1`).
		WithArgs("missing@test.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	exists, err := repo.ExistsByEmail(context.Background(), "missing@test.com")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ExistsByEmail_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT count\(\*\) FROM "users" WHERE email = \$1`).
		WithArgs("fail@test.com").
		WillReturnError(errors.New("email error"))

	_, err := repo.ExistsByEmail(context.Background(), "fail@test.com")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- ListAllIDs ----------

func TestUserRepo_ListAllIDs(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT "id" FROM "users"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3))

	ids, err := repo.ListAllIDs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2, 3}, ids)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ListAllIDs_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT "id" FROM "users"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	ids, err := repo.ListAllIDs(context.Background())
	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_ListAllIDs_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT "id" FROM "users"`).
		WillReturnError(errors.New("list error"))

	ids, err := repo.ListAllIDs(context.Background())
	assert.Error(t, err)
	assert.Nil(t, ids)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- UpdateBalance ----------

func TestUserRepo_UpdateBalance(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectExec(`UPDATE users SET balance = balance \+ \$1 WHERE id = \$2 AND balance \+ \$3 >= 0`).
		WithArgs(10.5, int64(1), 10.5).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectQuery(`SELECT "balance" FROM "users" WHERE "users"."id" = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(10.5))

	balance, err := repo.UpdateBalance(context.Background(), 1, 10.5)
	require.NoError(t, err)
	assert.Equal(t, 10.5, balance)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_UpdateBalance_Insufficient(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectExec(`UPDATE users SET balance = balance \+ \$1 WHERE id = \$2 AND balance \+ \$3 >= 0`).
		WithArgs(-50.0, int64(1), -50.0).
		WillReturnResult(sqlmock.NewResult(0, 0))

	balance, err := repo.UpdateBalance(context.Background(), 1, -50.0)
	assert.ErrorIs(t, err, ErrInsufficientBalance)
	assert.Equal(t, float64(0), balance)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_UpdateBalance_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectExec(`UPDATE users SET balance = balance \+ \$1 WHERE id = \$2 AND balance \+ \$3 >= 0`).
		WithArgs(5.0, int64(1), 5.0).
		WillReturnError(errors.New("update error"))

	balance, err := repo.UpdateBalance(context.Background(), 1, 5.0)
	assert.Error(t, err)
	assert.Equal(t, float64(0), balance)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- GetBalance ----------

func TestUserRepo_GetBalance(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT "balance" FROM "users" WHERE "users"."id" = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(42.5))

	balance, err := repo.GetBalance(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, 42.5, balance)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetBalance_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT "balance" FROM "users" WHERE "users"."id" = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs(int64(999), 1).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}))

	balance, err := repo.GetBalance(context.Background(), 999)
	assert.Error(t, err)
	assert.Equal(t, float64(0), balance)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUserRepo_GetBalance_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewUserRepo(db)

	mock.ExpectQuery(`SELECT "balance" FROM "users" WHERE "users"."id" = \$1 ORDER BY "users"."id" LIMIT \$2`).
		WithArgs(int64(1), 1).
		WillReturnError(errors.New("balance error"))

	balance, err := repo.GetBalance(context.Background(), 1)
	assert.Error(t, err)
	assert.Equal(t, float64(0), balance)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- IsNotFound ----------

func TestIsNotFound_True(t *testing.T) {
	assert.True(t, IsNotFound(gorm.ErrRecordNotFound))
}

func TestIsNotFound_False_Nil(t *testing.T) {
	assert.False(t, IsNotFound(nil))
}

func TestIsNotFound_False_Other(t *testing.T) {
	assert.False(t, IsNotFound(errors.New("some other error")))
}
