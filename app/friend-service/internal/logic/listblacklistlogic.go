package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListBlacklistLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListBlacklistLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListBlacklistLogic {
	return &ListBlacklistLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListBlacklistLogic) ListBlacklist(in *friend.ListBlacklistReq) (*friend.ListBlacklistResp, error) {
	userID := svc.UserIDFromCtx(l.ctx)
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

	userMap, _ := l.svcCtx.UserClient.BatchGetUserInfo(l.ctx, userIDs)

	items := make([]*friend.BlacklistUser, 0, len(blocks))
	for _, b := range blocks {
		bu := &friend.BlacklistUser{
			UserId:    b.BlockedUserID,
			BlockedAt: b.CreatedAt.Unix(),
		}
		if u, ok := userMap[b.BlockedUserID]; ok {
			bu.Username = u.GetUsername()
			bu.Avatar = u.GetAvatar()
		}
		items = append(items, bu)
	}

	return &friend.ListBlacklistResp{
		Users:      items,
		Pagination: paginationResp(page, pageSize, total),
	}, nil
}
