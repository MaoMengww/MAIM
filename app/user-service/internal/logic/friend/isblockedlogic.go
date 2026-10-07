package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/core/logx"
)

type IsBlockedLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewIsBlockedLogic(ctx context.Context, svcCtx *Context) *IsBlockedLogic {
	return &IsBlockedLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *IsBlockedLogic) IsBlocked(in *userpb.IsBlockedReq) (*userpb.IsBlockedResp, error) {
	if err := validateCaller(l.ctx, in.GetUserId(), in.GetTargetUserId()); err != nil {
		return nil, err
	}
	if in.GetUserId() == "" {
		return nil, grpcError(ErrInvalidParam)
	}
	blocked, err := l.svcCtx.BlockRepo.IsBlocked(l.ctx, in.GetUserId(), in.GetTargetUserId())
	if err != nil {
		return nil, grpcError(err)
	}

	return &userpb.IsBlockedResp{IsBlocked: blocked}, nil
}
