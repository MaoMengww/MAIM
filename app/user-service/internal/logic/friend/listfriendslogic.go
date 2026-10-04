package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListFriendsLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewListFriendsLogic(ctx context.Context, svcCtx *Context) *ListFriendsLogic {
	return &ListFriendsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListFriendsLogic) ListFriends(in *userpb.ListFriendsReq) (*userpb.ListFriendsResp, error) {
	userID := userIDFromContext(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	page, pageSize := paginationParams(in.GetPagination())
	offset, limit := paginationParams(in.GetPagination())

	var groupID *int64
	if in.GroupId != nil {
		groupID = in.GroupId
	}

	friends, total, err := l.svcCtx.FriendRepo.List(l.ctx, userID, groupID, offset, limit)
	if err != nil {
		return nil, grpcError(err)
	}

	friendIDs := make([]int64, len(friends))
	groupIDs := make([]int64, 0)
	for i, f := range friends {
		friendIDs[i] = f.FriendID
		if f.GroupID > 0 {
			groupIDs = append(groupIDs, f.GroupID)
		}
	}

	userMap, _ := l.svcCtx.batchGetUserInfo(l.ctx, friendIDs)

	groupNames := make(map[int64]string)
	for _, gid := range groupIDs {
		g, err := l.svcCtx.FriendGroupRepo.GetByID(l.ctx, gid)
		if err == nil {
			groupNames[gid] = g.Name
		}
	}

	items := make([]*userpb.FriendInfo, 0, len(friends))
	for _, f := range friends {
		info := &userpb.FriendInfo{
			UserId:    f.FriendID,
			Remark:    f.Remark,
			GroupId:   f.GroupID,
			GroupName: groupNames[f.GroupID],
			CreatedAt: f.CreatedAt.Unix(),
		}
		if u, ok := userMap[f.FriendID]; ok {
			info.Username = u.GetUsername()
			info.Avatar = u.GetAvatar()
		}
		items = append(items, info)
	}

	l.Infof("friends listed: user_id=%d count=%d", userID, len(friends))
	return &userpb.ListFriendsResp{
		Friends:    items,
		Pagination: paginationResp(page, pageSize, total),
	}, nil
}
