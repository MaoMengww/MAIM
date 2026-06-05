package logic

import (
	"context"

	"github.com/maomeng/aim/app/audit-service/internal/model"
	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type RecordAuditLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRecordAuditLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RecordAuditLogic {
	return &RecordAuditLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *RecordAuditLogic) RecordAudit(in *audit.RecordAuditReq) (*common.BaseResponse, error) {
	logger := l.WithContext(l.ctx)

	eventID := l.svcCtx.Snowflake.GenerateString()

	event := &model.AuditEvent{
		EventID:        eventID,
		Action:         int32(in.Action),
		Result:         int32(in.Result),
		Risk:           int32(in.Risk),
		UserID:         in.UserId,
		DeviceID:       in.GetDeviceId(),
		IPAddress:      in.GetIpAddress(),
		UserAgent:      in.GetUserAgent(),
		ResourceType:   in.GetResourceType(),
		ResourceID:     in.GetResourceId(),
		Detail:         model.JSONMap{},
		ErrorMessage:   in.GetErrorMessage(),
		TraceID:        in.GetTraceId(),
		SpanID:         in.GetSpanId(),
		ServiceName:    in.ServiceName,
		ServiceVersion: in.GetServiceVersion(),
	}

	if in.Detail != nil {
		event.Detail["raw"] = *in.Detail
	}

	if err := l.svcCtx.AuditRepo.Insert(l.ctx, event); err != nil {
		logger.Errorf("record audit event failed: event_id=%s err=%v", eventID, err)
		return &common.BaseResponse{Code: 500, Message: "insert audit event failed"}, nil
	}

	logger.Infof("audit event recorded: event_id=%s action=%s service=%s", eventID, in.Action.String(), in.ServiceName)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
