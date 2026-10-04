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
	userID := userIDFromContext(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	groups, err := l.svcCtx.FriendGroupRepo.List(l.ctx, userID)
	if err != nil {
		return nil, grpcError(err)
	}

	// count friends per group
	allFriends, _, _ := l.svcCtx.FriendRepo.List(l.ctx, userID, nil, 0, 10000)
	groupCount := make(map[int64]int32)
	for _, f := range allFriends {
		groupCount[f.GroupID]++
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
