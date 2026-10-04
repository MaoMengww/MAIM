package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteGroupLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewDeleteGroupLogic(ctx context.Context, svcCtx *Context) *DeleteGroupLogic {
	return &DeleteGroupLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *DeleteGroupLogic) DeleteGroup(in *userpb.DeleteGroupReq) (*common.BaseResponse, error) {
	userID := userIDFromContext(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	g, err := l.svcCtx.FriendGroupRepo.GetByID(l.ctx, in.GetGroupId())
	if err != nil {
		return nil, grpcError(ErrGroupNotFound)
	}
	if g.UserID != userID {
		return nil, grpcError(ErrNotGroupOwner)
	}

	if err := l.svcCtx.FriendGroupRepo.Delete(l.ctx, in.GetGroupId()); err != nil {
		return nil, grpcError(err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
