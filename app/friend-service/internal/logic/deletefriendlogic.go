package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteFriendLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteFriendLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteFriendLogic {
	return &DeleteFriendLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *DeleteFriendLogic) DeleteFriend(in *friend.DeleteFriendReq) (*common.BaseResponse, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	if ok, _ := l.svcCtx.FriendRepo.IsFriend(l.ctx, userID, in.GetFriendId()); !ok {
		return nil, grpcError(ErrNotFriend)
	}

	if err := l.svcCtx.FriendRepo.DeletePair(l.ctx, userID, in.GetFriendId()); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("friend deleted: user_id=%d friend_id=%d", userID, in.GetFriendId())
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
