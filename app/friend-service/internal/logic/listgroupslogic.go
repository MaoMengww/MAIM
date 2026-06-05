package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListGroupsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListGroupsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListGroupsLogic {
	return &ListGroupsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListGroupsLogic) ListGroups(in *friend.ListGroupsReq) (*friend.ListGroupsResp, error) {
	userID := svc.UserIDFromCtx(l.ctx)
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

	items := make([]*friend.FriendGroup, 0, len(groups))
	for _, g := range groups {
		items = append(items, &friend.FriendGroup{
			Id:          g.ID,
			Name:        g.Name,
			SortOrder:   g.SortOrder,
			FriendCount: groupCount[g.ID],
			CreatedAt:   g.CreatedAt.Unix(),
		})
	}

	return &friend.ListGroupsResp{Groups: items}, nil
}
