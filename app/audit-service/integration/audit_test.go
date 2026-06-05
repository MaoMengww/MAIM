//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/audit-service/internal/config"
	"github.com/maomeng/aim/app/audit-service/internal/logic"
	"github.com/maomeng/aim/app/audit-service/internal/svc"
	auditpb "github.com/maomeng/aim/app/audit-service/pb/audit"
	"github.com/maomeng/aim/pkg/configcenter"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/conf"
)

func newAuditSvcCtx(t *testing.T) *svc.ServiceContext {
	t.Helper()
	var c config.Config
	conf.MustLoad("../etc/audit.yaml", &c)
	c.Telemetry.Endpoint = ""
	c.Kafka.Brokers = nil
	c.MinIO.Endpoint = ""

	if raw := etcdRawConfig("audit.rpc"); raw != nil {
		configcenter.MergeRemote(&c, raw)
	}

	return svc.NewServiceContext(c)
}

func TestRecordAudit(t *testing.T) {
	svcCtx := newAuditSvcCtx(t)
	ctx := context.Background()

	recordLogic := logic.NewRecordAuditLogic(ctx, svcCtx)
	_, err := recordLogic.RecordAudit(&auditpb.RecordAuditReq{
		Action:      auditpb.AuditAction_AUDIT_ACTION_LOGIN,
		Result:      auditpb.AuditResult_AUDIT_RESULT_SUCCESS,
		Risk:        auditpb.AuditRisk_AUDIT_RISK_LOW,
		UserId:      1001,
		ServiceName: "test-service",
		IpAddress:   strPtr("127.0.0.1"),
	})
	require.NoError(t, err)
}

func TestBatchRecordAudit(t *testing.T) {
	svcCtx := newAuditSvcCtx(t)
	ctx := context.Background()

	batchLogic := logic.NewBatchRecordAuditLogic(ctx, svcCtx)
	_, err := batchLogic.BatchRecordAudit(&auditpb.BatchRecordAuditReq{
		Events: []*auditpb.RecordAuditReq{
			{
				Action:      auditpb.AuditAction_AUDIT_ACTION_LOGIN,
				Result:      auditpb.AuditResult_AUDIT_RESULT_SUCCESS,
				Risk:        auditpb.AuditRisk_AUDIT_RISK_LOW,
				UserId:      1001,
				ServiceName: "test-service",
			},
			{
				Action:      auditpb.AuditAction_AUDIT_ACTION_SEND_MESSAGE,
				Result:      auditpb.AuditResult_AUDIT_RESULT_SUCCESS,
				Risk:        auditpb.AuditRisk_AUDIT_RISK_LOW,
				UserId:      1002,
				ServiceName: "test-service",
			},
		},
	})
	require.NoError(t, err)
}

func TestListAudits(t *testing.T) {
	svcCtx := newAuditSvcCtx(t)
	ctx := context.Background()

	recordLogic := logic.NewRecordAuditLogic(ctx, svcCtx)
	_, err := recordLogic.RecordAudit(&auditpb.RecordAuditReq{
		Action:      auditpb.AuditAction_AUDIT_ACTION_LOGOUT,
		Result:      auditpb.AuditResult_AUDIT_RESULT_SUCCESS,
		Risk:        auditpb.AuditRisk_AUDIT_RISK_LOW,
		UserId:      2001,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	listLogic := logic.NewListAuditsLogic(ctx, svcCtx)
	listResp, err := listLogic.ListAudits(&auditpb.ListAuditsReq{
		UserId:     int64Ptr(2001),
		Pagination: &common.Pagination{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(listResp.Events), 1)
}

func TestGetAudit(t *testing.T) {
	svcCtx := newAuditSvcCtx(t)
	ctx := context.Background()

	recordLogic := logic.NewRecordAuditLogic(ctx, svcCtx)
	_, err := recordLogic.RecordAudit(&auditpb.RecordAuditReq{
		Action:      auditpb.AuditAction_AUDIT_ACTION_REGISTER,
		Result:      auditpb.AuditResult_AUDIT_RESULT_SUCCESS,
		Risk:        auditpb.AuditRisk_AUDIT_RISK_LOW,
		UserId:      3001,
		ServiceName: "test-service",
	})
	require.NoError(t, err)

	listLogic := logic.NewListAuditsLogic(ctx, svcCtx)
	listResp, err := listLogic.ListAudits(&auditpb.ListAuditsReq{
		UserId:     int64Ptr(3001),
		Pagination: &common.Pagination{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.Greater(t, len(listResp.Events), 0)
	eventID := listResp.Events[0].EventId

	getLogic := logic.NewGetAuditLogic(ctx, svcCtx)
	getResp, err := getLogic.GetAudit(&auditpb.GetAuditReq{
		EventId: eventID,
	})
	require.NoError(t, err)
	assert.Equal(t, eventID, getResp.Event.EventId)
	assert.Equal(t, auditpb.AuditAction_AUDIT_ACTION_REGISTER, getResp.Event.Action)
}
