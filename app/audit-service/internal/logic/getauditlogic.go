package logic

import (
	"context"

	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type GetAuditLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetAuditLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetAuditLogic {
	return &GetAuditLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetAuditLogic) GetAudit(in *audit.GetAuditReq) (*audit.GetAuditResp, error) {
	logger := l.WithContext(l.ctx)

	event, err := l.svcCtx.AuditRepo.GetByEventID(l.ctx, in.EventId)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, err
		}
		logger.Errorf("get audit event failed: event_id=%s err=%v", in.EventId, err)
		return nil, err
	}

	return &audit.GetAuditResp{
		Event: modelToPb(event),
	}, nil
}
