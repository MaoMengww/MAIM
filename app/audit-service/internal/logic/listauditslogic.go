package logic

import (
	"context"

	"github.com/maomeng/aim/app/audit-service/internal/repo"
	"github.com/maomeng/aim/app/audit-service/internal/svc"
	"github.com/maomeng/aim/app/audit-service/pb/audit"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListAuditsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListAuditsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListAuditsLogic {
	return &ListAuditsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListAuditsLogic) ListAudits(in *audit.ListAuditsReq) (*audit.ListAuditsResp, error) {
	logger := l.WithContext(l.ctx)

	f := repo.ListFilter{
		Page:     int(in.GetPagination().GetPage()),
		PageSize: int(in.GetPagination().GetPageSize()),
	}

	if in.UserId != nil {
		f.UserID = in.UserId
	}
	if in.Action != nil {
		v := int32(*in.Action)
		f.Action = &v
	}
	if in.Result != nil {
		v := int32(*in.Result)
		f.Result = &v
	}
	if in.Risk != nil {
		v := int32(*in.Risk)
		f.Risk = &v
	}
	if in.ResourceType != nil {
		f.ResourceType = in.ResourceType
	}
	if in.ResourceId != nil {
		f.ResourceID = in.ResourceId
	}
	if in.StartTime != nil {
		f.StartTime = in.StartTime
	}
	if in.EndTime != nil {
		f.EndTime = in.EndTime
	}
	if in.IpAddress != nil {
		f.IPAddress = in.IpAddress
	}
	if in.ServiceName != nil {
		f.ServiceName = in.ServiceName
	}

	result, err := l.svcCtx.AuditRepo.List(l.ctx, f)
	if err != nil {
		logger.Errorf("list audit events failed: err=%v", err)
		return nil, err
	}

	logger.Infof("audits listed: total=%d page=%d page_size=%d", result.Total, result.Page, result.PageSize)

	events := make([]*audit.AuditEvent, 0, len(result.Events))
	for _, e := range result.Events {
		events = append(events, modelToPb(&e))
	}

	return &audit.ListAuditsResp{
		Events: events,
		Pagination: &common.PaginationResp{
			Page:       int32(result.Page),
			PageSize:   int32(result.PageSize),
			Total:      result.Total,
			TotalPages: int32(result.TotalPage),
		},
	}, nil
}
