package logic

import (
	"context"

	"github.com/maomeng/aim/app/audit-service/internal/model"
	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type BatchRecordAuditLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewBatchRecordAuditLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchRecordAuditLogic {
	return &BatchRecordAuditLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *BatchRecordAuditLogic) BatchRecordAudit(in *audit.BatchRecordAuditReq) (*common.BaseResponse, error) {
	logger := l.WithContext(l.ctx)

	if len(in.Events) == 0 {
		return &common.BaseResponse{Code: 0, Message: "ok"}, nil
	}

	events := make([]*model.AuditEvent, 0, len(in.Events))
	for _, req := range in.Events {
		eventID := l.svcCtx.Snowflake.GenerateString()
		event := &model.AuditEvent{
			EventID:        eventID,
			Action:         int32(req.Action),
			Result:         int32(req.Result),
			Risk:           int32(req.Risk),
			UserID:         req.UserId,
			DeviceID:       req.GetDeviceId(),
			IPAddress:      req.GetIpAddress(),
			UserAgent:      req.GetUserAgent(),
			ResourceType:   req.GetResourceType(),
			ResourceID:     req.GetResourceId(),
			Detail:         model.JSONMap{},
			ErrorMessage:   req.GetErrorMessage(),
			TraceID:        req.GetTraceId(),
			SpanID:         req.GetSpanId(),
			ServiceName:    req.ServiceName,
			ServiceVersion: req.GetServiceVersion(),
		}
		if req.Detail != nil {
			event.Detail["raw"] = *req.Detail
		}
		events = append(events, event)
	}

	if err := l.svcCtx.AuditRepo.BatchInsert(l.ctx, events); err != nil {
		logger.Errorf("batch record audit events failed: count=%d err=%v", len(events), err)
		return &common.BaseResponse{Code: 500, Message: "batch insert audit events failed"}, nil
	}

	logger.Infof("batch audit events recorded: count=%d", len(events))
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
