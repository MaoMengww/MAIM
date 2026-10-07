package friend

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"

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
	if in.GroupId != nil && identity.Validate(*in.GroupId) != nil {
		return nil, grpcError(ErrInvalidParam)
	}
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

	if in.ClearGroupId && in.GroupId != nil {
		return nil, grpcError(ErrInvalidParam)
	}
	if in.GroupId == nil && !in.ClearGroupId {
		return &common.BaseResponse{Code: 0, Message: "ok"}, nil
	}
	if in.GroupId != nil {
		g, err := l.svcCtx.FriendGroupRepo.GetByID(l.ctx, in.GetGroupId())
		if err != nil || g.UserID != userID {
			return nil, grpcError(ErrGroupNotFound)
		}
	}

	if err := l.svcCtx.FriendRepo.UpdateGroup(l.ctx, userID, in.GetFriendId(), in.GroupId); err != nil {
		return nil, grpcError(err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
