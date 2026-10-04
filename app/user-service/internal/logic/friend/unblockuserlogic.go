package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type UnblockUserLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewUnblockUserLogic(ctx context.Context, svcCtx *Context) *UnblockUserLogic {
	return &UnblockUserLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *UnblockUserLogic) UnblockUser(in *userpb.UnblockUserReq) (*common.BaseResponse, error) {
	userID := userIDFromContext(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	if err := l.svcCtx.BlockRepo.Delete(l.ctx, userID, in.GetBlockedUserId()); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("user unblocked: user_id=%d blocked_id=%d", userID, in.GetBlockedUserId())
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
