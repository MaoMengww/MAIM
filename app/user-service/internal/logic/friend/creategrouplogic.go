package friend

import (
	"context"
	"strings"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/core/logx"
)

type CreateGroupLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewCreateGroupLogic(ctx context.Context, svcCtx *Context) *CreateGroupLogic {
	return &CreateGroupLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *CreateGroupLogic) CreateGroup(in *userpb.CreateGroupReq) (*userpb.CreateGroupResp, error) {
	if err := validateCaller(l.ctx, in.GetUserId()); err != nil {
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

	groupID, err := l.svcCtx.FriendGroupRepo.Create(l.ctx, userID, name)
	if err != nil {
		return nil, grpcError(err)
	}

	return &userpb.CreateGroupResp{GroupId: groupID}, nil
}
