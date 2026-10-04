package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type SetGroupLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewSetGroupLogic(ctx context.Context, svcCtx *Context) *SetGroupLogic {
	return &SetGroupLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SetGroupLogic) SetGroup(in *userpb.SetGroupReq) (*common.BaseResponse, error) {
	userID := userIDFromContext(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	if ok, _ := l.svcCtx.FriendRepo.IsFriend(l.ctx, userID, in.GetFriendId()); !ok {
		return nil, grpcError(ErrNotFriend)
	}

	if in.GetGroupId() != 0 {
		g, err := l.svcCtx.FriendGroupRepo.GetByID(l.ctx, in.GetGroupId())
		if err != nil || g.UserID != userID {
			return nil, grpcError(ErrGroupNotFound)
		}
	}

	if err := l.svcCtx.FriendRepo.UpdateGroup(l.ctx, userID, in.GetFriendId(), in.GetGroupId()); err != nil {
		return nil, grpcError(err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
