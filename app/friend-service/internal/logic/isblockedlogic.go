package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"

	"github.com/zeromicro/go-zero/core/logx"
)

type IsBlockedLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewIsBlockedLogic(ctx context.Context, svcCtx *svc.ServiceContext) *IsBlockedLogic {
	return &IsBlockedLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *IsBlockedLogic) IsBlocked(in *friend.IsBlockedReq) (*friend.IsBlockedResp, error) {
	if in.GetUserId() == 0 {
		return nil, grpcError(ErrInvalidParam)
	}
	blocked, err := l.svcCtx.BlockRepo.IsBlocked(l.ctx, in.GetUserId(), in.GetTargetUserId())
	if err != nil {
		return nil, grpcError(err)
	}

	return &friend.IsBlockedResp{IsBlocked: blocked}, nil
}
