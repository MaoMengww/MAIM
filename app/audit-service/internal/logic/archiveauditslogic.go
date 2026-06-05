package logic

import (
	"context"

	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type ArchiveAuditsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewArchiveAuditsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ArchiveAuditsLogic {
	return &ArchiveAuditsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ArchiveAuditsLogic) ArchiveAudits(in *audit.ArchiveAuditsReq) (*common.BaseResponse, error) {
	logger := l.WithContext(l.ctx)

	if in.BeforeTime <= 0 {
		return &common.BaseResponse{Code: 400, Message: "before_time is required"}, nil
	}

	count, err := l.svcCtx.AuditRepo.Archive(l.ctx, in.BeforeTime, in.DeleteAfterArchive)
	if err != nil {
		logger.Errorf("archive audit events failed: err=%v", err)
		return &common.BaseResponse{Code: 500, Message: "archive audit events failed"}, nil
	}

	logger.Infof("audit archive completed: before_time=%d count=%d delete=%v", in.BeforeTime, count, in.DeleteAfterArchive)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
