package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListGroupsLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewListGroupsLogic(ctx context.Context, svcCtx *Context) *ListGroupsLogic {
	return &ListGroupsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListGroupsLogic) ListGroups(in *userpb.ListGroupsReq) (*userpb.ListGroupsResp, error) {
	if err := validateCaller(l.ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	userID := userIDFromContext(l.ctx)
	if userID == "" {
		return nil, grpcError(ErrUnauthenticated)
	}

	groups, err := l.svcCtx.FriendGroupRepo.List(l.ctx, userID)
	if err != nil {
		return nil, grpcError(err)
	}

	// count friends per group
	allFriends, _, err := l.svcCtx.FriendRepo.List(l.ctx, userID, nil, 0, -1)
	if err != nil {
		return nil, grpcError(err)
	}
	groupCount := make(map[string]int32)
	for _, f := range allFriends {
		if f.GroupID != nil {
			groupCount[*f.GroupID]++
		}
	}

	items := make([]*userpb.FriendGroup, 0, len(groups))
	for _, g := range groups {
		items = append(items, &userpb.FriendGroup{
			Id:          g.ID,
			Name:        g.Name,
			SortOrder:   g.SortOrder,
			FriendCount: groupCount[g.ID],
			CreatedAt:   g.CreatedAt.Unix(),
		})
	}

	return &userpb.ListGroupsResp{Groups: items}, nil
}
