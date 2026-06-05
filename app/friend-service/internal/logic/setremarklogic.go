package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type SetRemarkLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSetRemarkLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SetRemarkLogic {
	return &SetRemarkLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SetRemarkLogic) SetRemark(in *friend.SetRemarkReq) (*common.BaseResponse, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	if ok, _ := l.svcCtx.FriendRepo.IsFriend(l.ctx, userID, in.GetFriendId()); !ok {
		return nil, grpcError(ErrNotFriend)
	}

	if err := l.svcCtx.FriendRepo.UpdateRemark(l.ctx, userID, in.GetFriendId(), in.GetRemark()); err != nil {
		return nil, grpcError(err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
