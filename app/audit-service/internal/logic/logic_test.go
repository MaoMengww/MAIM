package logic

import (
	"context"
	"testing"
	"time"

	"github.com/maomeng/aim/app/audit-service/internal/config"
	"github.com/maomeng/aim/app/audit-service/internal/model"
	"github.com/maomeng/aim/app/audit-service/internal/repo"
	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/maomeng/aim/pkg/database"
)

func setupSvcCtx(t *testing.T) (*svc.ServiceContext, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	db := &database.DB{DB: gormDB}

	sf, err := snowflake.NewNode(1)
	require.NoError(t, err)

	return &svc.ServiceContext{
		Config:    config.Config{},
		DB:        db,
		AuditRepo: repo.NewAuditRepo(db),
		Snowflake: sf,
	}, mock
}

func TestRecordAuditLogic(t *testing.T) {
	svcCtx, mock := setupSvcCtx(t)
	l := NewRecordAuditLogic(context.Background(), svcCtx)

	req := &audit.RecordAuditReq{
		Action:      audit.AuditAction_AUDIT_ACTION_LOGIN,
		Result:      audit.AuditResult_AUDIT_RESULT_SUCCESS,
		Risk:        audit.AuditRisk_AUDIT_RISK_LOW,
		UserId:      1001,
		ServiceName: "user-service",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "audit_events"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	resp, err := l.RecordAudit(req)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
	assert.Equal(t, "ok", resp.Message)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchRecordAuditLogic(t *testing.T) {
	svcCtx, mock := setupSvcCtx(t)
	l := NewBatchRecordAuditLogic(context.Background(), svcCtx)

	req := &audit.BatchRecordAuditReq{
		Events: []*audit.RecordAuditReq{
			{Action: audit.AuditAction_AUDIT_ACTION_LOGIN, UserId: 1001, ServiceName: "svc"},
			{Action: audit.AuditAction_AUDIT_ACTION_LOGOUT, UserId: 1002, ServiceName: "svc"},
		},
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "audit_events"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	resp, err := l.BatchRecordAudit(req)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchRecordAuditLogic_Empty(t *testing.T) {
	svcCtx, _ := setupSvcCtx(t)
	l := NewBatchRecordAuditLogic(context.Background(), svcCtx)

	req := &audit.BatchRecordAuditReq{}
	resp, err := l.BatchRecordAudit(req)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
}

func TestArchiveAuditsLogic(t *testing.T) {
	svcCtx, mock := setupSvcCtx(t)
	l := NewArchiveAuditsLogic(context.Background(), svcCtx)

	req := &audit.ArchiveAuditsReq{
		BeforeTime:         1700000000,
		DeleteAfterArchive: false,
	}

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE "audit_events"`).
		WillReturnResult(sqlmock.NewResult(0, 100))
	mock.ExpectCommit()

	resp, err := l.ArchiveAudits(req)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), resp.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestArchiveAuditsLogic_InvalidTime(t *testing.T) {
	svcCtx, _ := setupSvcCtx(t)
	l := NewArchiveAuditsLogic(context.Background(), svcCtx)

	req := &audit.ArchiveAuditsReq{BeforeTime: 0}
	resp, err := l.ArchiveAudits(req)
	assert.NoError(t, err)
	assert.Equal(t, int32(400), resp.Code)
}

func TestModelToPb(t *testing.T) {
	now := time.Now()
	e := &model.AuditEvent{
		EventID:   "evt-001",
		Action:    1,
		Result:    1,
		Risk:      1,
		UserID:    1001,
		CreatedAt: now,
	}

	pb := modelToPb(e)
	assert.Equal(t, "evt-001", pb.EventId)
	assert.Equal(t, audit.AuditAction(1), pb.Action)
	assert.Equal(t, int64(1001), pb.UserId)
	assert.Equal(t, now.Unix(), pb.CreatedAt)
}

func TestBaseResponse(t *testing.T) {
	resp := &common.BaseResponse{Code: 0, Message: "ok"}
	assert.Equal(t, int32(0), resp.Code)
	assert.Equal(t, "ok", resp.Message)
}
