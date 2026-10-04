package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type SetRemarkLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewSetRemarkLogic(ctx context.Context, svcCtx *Context) *SetRemarkLogic {
	return &SetRemarkLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SetRemarkLogic) SetRemark(in *userpb.SetRemarkReq) (*common.BaseResponse, error) {
	userID := userIDFromContext(l.ctx)
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
