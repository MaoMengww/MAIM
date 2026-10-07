package friend

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"

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
	if in.GroupId != nil && identity.Validate(*in.GroupId) != nil {
		return nil, grpcError(ErrInvalidParam)
	}
	if err := validateCaller(l.ctx, in.GetUserId()); err != nil {
		return nil, err
	}
	userID := userIDFromContext(l.ctx)
	if userID == "" {
		return nil, grpcError(ErrUnauthenticated)
	}

	page, pageSize := 1, 20
	if p := in.GetPagination(); p != nil {
		page = max(1, int(p.Page))
		if p.PageSize >= 1 && p.PageSize <= 100 {
			pageSize = int(p.PageSize)
		}
	}
	offset, limit := paginationParams(in.GetPagination())

	var groupID *string
	if in.GroupId != nil {
		groupID = in.GroupId
	}

	friends, total, err := l.svcCtx.FriendRepo.List(l.ctx, userID, groupID, offset, limit)
	if err != nil {
		return nil, grpcError(err)
	}

	friendIDs := make([]string, len(friends))
	groupIDs := make([]string, 0)
	for i, f := range friends {
		friendIDs[i] = f.FriendID
		if f.GroupID != nil {
			groupIDs = append(groupIDs, *f.GroupID)
		}
	}

	userMap, err := l.svcCtx.batchGetUserInfo(l.ctx, friendIDs)
	if err != nil {
		return nil, grpcError(err)
	}

	groupNames := make(map[string]string)
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
			CreatedAt: f.CreatedAt.Unix(),
		}
		if f.GroupID != nil {
			info.GroupName = groupNames[*f.GroupID]
		}
		if u, ok := userMap[f.FriendID]; ok {
			info.Username = u.GetUsername()
			info.Avatar = u.GetAvatar()
		}
		items = append(items, info)
	}

	l.Infof("friends listed: user_id=%s count=%d", userID, len(friends))
	return &userpb.ListFriendsResp{
		Friends:    items,
		Pagination: paginationResp(page, pageSize, total),
	}, nil
}
