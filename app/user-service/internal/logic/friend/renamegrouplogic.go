package friend

import (
	"context"
	"strings"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type RenameGroupLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewRenameGroupLogic(ctx context.Context, svcCtx *Context) *RenameGroupLogic {
	return &RenameGroupLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *RenameGroupLogic) RenameGroup(in *userpb.RenameGroupReq) (*common.BaseResponse, error) {
	if err := validateCaller(l.ctx, in.GetUserId(), in.GetGroupId()); err != nil {
		return nil, err
	}
	userID := userIDFromContext(l.ctx)
	if userID == "" {
		return nil, grpcError(ErrUnauthenticated)
	}
	name := strings.TrimSpace(in.GetName())
	if name == "" {
		return nil, grpcError(ErrInvalidParam)
	}

	g, err := l.svcCtx.FriendGroupRepo.GetByID(l.ctx, in.GetGroupId())
	if err != nil {
		return nil, grpcError(ErrGroupNotFound)
	}
	if g.UserID != userID {
		return nil, grpcError(ErrNotGroupOwner)
	}

	if err := l.svcCtx.FriendGroupRepo.Update(l.ctx, in.GetGroupId(), name); err != nil {
		return nil, grpcError(err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
