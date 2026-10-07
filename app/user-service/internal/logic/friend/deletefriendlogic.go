package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteFriendLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewDeleteFriendLogic(ctx context.Context, svcCtx *Context) *DeleteFriendLogic {
	return &DeleteFriendLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *DeleteFriendLogic) DeleteFriend(in *userpb.DeleteFriendReq) (*common.BaseResponse, error) {
	if err := validateCaller(l.ctx, in.GetUserId(), in.GetFriendId()); err != nil {
		return nil, err
	}
	userID := userIDFromContext(l.ctx)
	if userID == "" {
		return nil, grpcError(ErrUnauthenticated)
	}

	if ok, err := l.svcCtx.FriendRepo.IsFriend(l.ctx, userID, in.GetFriendId()); err != nil {
		return nil, grpcError(err)
	} else if !ok {
		return nil, grpcError(ErrNotFriend)
	}

	if err := l.svcCtx.FriendRepo.DeletePair(l.ctx, userID, in.GetFriendId()); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("friend deleted: user_id=%s friend_id=%s", userID, in.GetFriendId())
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
