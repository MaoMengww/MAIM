package repo

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/app/audit-service/internal/model"
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

func TestAuditRepo_Insert(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuditRepo(db)
	event := &model.AuditEvent{
		EventID:   "evt-001",
		Action:    1,
		Result:    1,
		UserID:    1001,
		CreatedAt: time.Now(),
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "audit_events"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	err := repo.Insert(context.Background(), event)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditRepo_BatchInsert(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuditRepo(db)

	events := []*model.AuditEvent{
		{EventID: "evt-001", Action: 1},
		{EventID: "evt-002", Action: 2},
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "audit_events"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	err := repo.BatchInsert(context.Background(), events)
	assert.NoError(t, err)
}

func TestAuditRepo_BatchInsert_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuditRepo(db)
	err := repo.BatchInsert(context.Background(), nil)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditRepo_GetByEventID(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuditRepo(db)

	rows := sqlmock.NewRows([]string{
		"id", "event_id", "action", "result", "risk", "user_id", "device_id", "ip_address", "user_agent",
		"resource_type", "resource_id", "detail", "error_message", "trace_id", "span_id",
		"service_name", "service_version", "review_status", "review_result", "is_archived", "created_at",
	}).AddRow(1, "evt-001", 1, 1, 1, 1001, "", "", "", "", "", `{}`, "", "", "", "", "", 0, `{}`, false, time.Now())

	mock.ExpectQuery(`SELECT \* FROM "audit_events" WHERE event_id = \$1 AND is_archived = false ORDER BY "audit_events"."id" LIMIT \$2`).
		WithArgs("evt-001", 1).
		WillReturnRows(rows)

	event, err := repo.GetByEventID(context.Background(), "evt-001")
	assert.NoError(t, err)
	assert.Equal(t, "evt-001", event.EventID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditRepo_List(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuditRepo(db)

	countRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
	mock.ExpectQuery(`SELECT count\(\*\) FROM "audit_events" WHERE is_archived = false`).
		WillReturnRows(countRows)

	dataRows := sqlmock.NewRows([]string{
		"id", "event_id", "action", "result", "risk", "user_id", "device_id", "ip_address", "user_agent",
		"resource_type", "resource_id", "detail", "error_message", "trace_id", "span_id",
		"service_name", "service_version", "review_status", "review_result", "is_archived", "created_at",
	}).AddRow(1, "evt-001", 1, 1, 1, 1001, "", "", "", "", "", `{}`, "", "", "", "", "", 0, `{}`, false, time.Now())

	mock.ExpectQuery(`SELECT \* FROM "audit_events"`).
		WillReturnRows(dataRows)

	result, err := repo.List(context.Background(), ListFilter{Page: 1, PageSize: 20})
	assert.NoError(t, err)
	assert.Equal(t, int64(1), result.Total)
	assert.Equal(t, 1, result.TotalPage)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditRepo_Archive_MarkArchived(t *testing.T) {
	db, mock := setupMockDB(t)
	repo := NewAuditRepo(db)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "audit_events" SET "is_archived"=\$1`).
		WillReturnResult(sqlmock.NewResult(0, 10))
	mock.ExpectCommit()

	count, err := repo.Archive(context.Background(), time.Now().Unix(), false)
	assert.NoError(t, err)
	assert.Equal(t, int64(10), count)
	assert.NoError(t, mock.ExpectationsWereMet())
}
