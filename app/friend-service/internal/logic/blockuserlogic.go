package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type BlockUserLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewBlockUserLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BlockUserLogic {
	return &BlockUserLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *BlockUserLogic) BlockUser(in *friend.BlockUserReq) (*common.BaseResponse, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}
	targetID := in.GetBlockedUserId()
	if targetID == 0 || userID == targetID {
		return nil, grpcError(ErrInvalidParam)
	}

	if ok, _ := l.svcCtx.BlockRepo.IsBlocked(l.ctx, userID, targetID); ok {
		return &common.BaseResponse{Code: 0, Message: "ok"}, nil
	}

	if err := l.svcCtx.BlockRepo.Create(l.ctx, userID, targetID); err != nil {
		return nil, grpcError(err)
	}

	// remove friend relationship if exists
	l.svcCtx.FriendRepo.DeletePair(l.ctx, userID, targetID)

	l.Infof("user blocked: user_id=%d blocked_id=%d", userID, targetID)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
