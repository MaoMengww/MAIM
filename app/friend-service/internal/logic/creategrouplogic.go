package logic

import (
	"context"
	"strings"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"

	"github.com/zeromicro/go-zero/core/logx"
)

type CreateGroupLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewCreateGroupLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateGroupLogic {
	return &CreateGroupLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *CreateGroupLogic) CreateGroup(in *friend.CreateGroupReq) (*friend.CreateGroupResp, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
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

	return &friend.CreateGroupResp{GroupId: groupID}, nil
}
