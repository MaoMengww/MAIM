package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListBlacklistLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewListBlacklistLogic(ctx context.Context, svcCtx *Context) *ListBlacklistLogic {
	return &ListBlacklistLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListBlacklistLogic) ListBlacklist(in *userpb.ListBlacklistReq) (*userpb.ListBlacklistResp, error) {
	userID := userIDFromContext(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	page, pageSize := paginationParams(in.GetPagination())
	offset, limit := paginationParams(in.GetPagination())

	blocks, total, err := l.svcCtx.BlockRepo.List(l.ctx, userID, offset, limit)
	if err != nil {
		return nil, grpcError(err)
	}

	userIDs := make([]int64, len(blocks))
	for i, b := range blocks {
		userIDs[i] = b.BlockedUserID
	}

	userMap, _ := l.svcCtx.batchGetUserInfo(l.ctx, userIDs)

	items := make([]*userpb.BlacklistUser, 0, len(blocks))
	for _, b := range blocks {
		bu := &userpb.BlacklistUser{
			UserId:    b.BlockedUserID,
			BlockedAt: b.CreatedAt.Unix(),
		}
		if u, ok := userMap[b.BlockedUserID]; ok {
			bu.Username = u.GetUsername()
			bu.Avatar = u.GetAvatar()
		}
		items = append(items, bu)
	}

	return &userpb.ListBlacklistResp{
		Users:      items,
		Pagination: paginationResp(page, pageSize, total),
	}, nil
}
