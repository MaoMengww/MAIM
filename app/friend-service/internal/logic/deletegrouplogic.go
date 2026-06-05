package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteGroupLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewDeleteGroupLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteGroupLogic {
	return &DeleteGroupLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *DeleteGroupLogic) DeleteGroup(in *friend.DeleteGroupReq) (*common.BaseResponse, error) {
	userID := svc.UserIDFromCtx(l.ctx)
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
