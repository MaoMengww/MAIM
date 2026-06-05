package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var deviceColumns = []string{
	"id", "user_id", "device_id", "platform", "push_token", "ip", "location",
	"last_active_at", "created_at",
}

func deviceRow(id, userID int64, deviceID, platform string) []driverValue {
	now := time.Now()
	return []driverValue{
		id, userID, deviceID, platform, "", "", "",
		now, now,
	}
}

func TestNewAuthRepo(t *testing.T) {
	repo := NewAuthRepo(nil, nil)
	assert.NotNil(t, repo)
}

// ---------- SaveDevice ----------

func TestAuthRepo_SaveDevice_Create(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	dev := &model.UserDevice{
		UserID:   1,
		DeviceID: "device-001",
		Platform: "web",
	}

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2 ORDER BY "user_devices"."id" LIMIT \$3`).
		WithArgs(int64(1), "device-001", 1).
		WillReturnRows(sqlmock.NewRows(deviceColumns))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "user_devices"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	mock.ExpectCommit()

	err := repo.SaveDevice(context.Background(), dev)
	assert.NoError(t, err)
	assert.Equal(t, int64(10), dev.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthRepo_SaveDevice_Update(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	dev := &model.UserDevice{
		UserID:   1,
		DeviceID: "device-001",
		Platform: "mobile",
		IP:       "192.168.1.1",
	}

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2 ORDER BY "user_devices"."id" LIMIT \$3`).
		WithArgs(int64(1), "device-001", 1).
		WillReturnRows(sqlmock.NewRows(deviceColumns).AddRow(deviceRow(10, 1, "device-001", "web")...))

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "user_devices" SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.SaveDevice(context.Background(), dev)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthRepo_SaveDevice_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	dev := &model.UserDevice{
		UserID:   1,
		DeviceID: "device-001",
	}

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2 ORDER BY "user_devices"."id" LIMIT \$3`).
		WithArgs(int64(1), "device-001", 1).
		WillReturnError(errors.New("query failed"))

	err := repo.SaveDevice(context.Background(), dev)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "query failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- GetUserDevices ----------

func TestAuthRepo_GetUserDevices(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 ORDER BY last_active_at DESC`).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(deviceColumns).
			AddRow(deviceRow(1, 1, "device-a", "web")...).
			AddRow(deviceRow(2, 1, "device-b", "mobile")...))

	devices, err := repo.GetUserDevices(context.Background(), 1)
	require.NoError(t, err)
	assert.Len(t, devices, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthRepo_GetUserDevices_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 ORDER BY last_active_at DESC`).
		WithArgs(int64(999)).
		WillReturnRows(sqlmock.NewRows(deviceColumns))

	devices, err := repo.GetUserDevices(context.Background(), 999)
	require.NoError(t, err)
	assert.Empty(t, devices)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthRepo_GetUserDevices_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 ORDER BY last_active_at DESC`).
		WithArgs(int64(1)).
		WillReturnError(errors.New("query error"))

	devices, err := repo.GetUserDevices(context.Background(), 1)
	assert.Error(t, err)
	assert.Nil(t, devices)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- DeleteDevice ----------

func TestAuthRepo_DeleteDevice(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2`).
		WithArgs(int64(1), "device-001").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.DeleteDevice(context.Background(), 1, "device-001")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthRepo_DeleteDevice_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2`).
		WithArgs(int64(1), "device-001").
		WillReturnError(errors.New("delete error"))
	mock.ExpectRollback()

	err := repo.DeleteDevice(context.Background(), 1, "device-001")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete error")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------- GetDevice ----------

func TestAuthRepo_GetDevice(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2 ORDER BY "user_devices"."id" LIMIT \$3`).
		WithArgs(int64(1), "device-001", 1).
		WillReturnRows(sqlmock.NewRows(deviceColumns).AddRow(deviceRow(1, 1, "device-001", "web")...))

	dev, err := repo.GetDevice(context.Background(), 1, "device-001")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dev.ID)
	assert.Equal(t, "device-001", dev.DeviceID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthRepo_GetDevice_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2 ORDER BY "user_devices"."id" LIMIT \$3`).
		WithArgs(int64(999), "unknown", 1).
		WillReturnRows(sqlmock.NewRows(deviceColumns))

	dev, err := repo.GetDevice(context.Background(), 999, "unknown")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Nil(t, dev)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthRepo_GetDevice_DBError(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuthRepo(db, nil)

	mock.ExpectQuery(`SELECT \* FROM "user_devices" WHERE user_id = \$1 AND device_id = \$2 ORDER BY "user_devices"."id" LIMIT \$3`).
		WithArgs(int64(1), "device-001", 1).
		WillReturnError(errors.New("device error"))

	dev, err := repo.GetDevice(context.Background(), 1, "device-001")
	assert.Error(t, err)
	assert.Nil(t, dev)
	assert.NoError(t, mock.ExpectationsWereMet())
}
