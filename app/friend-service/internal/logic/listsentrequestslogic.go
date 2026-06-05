package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListSentRequestsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListSentRequestsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListSentRequestsLogic {
	return &ListSentRequestsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListSentRequestsLogic) ListSentRequests(in *friend.ListSentRequestsReq) (*friend.ListRequestsResp, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
		return nil, ErrUnauthenticated
	}

	offset, limit := paginationParams(in.GetPagination())

	page := 1
	if p := in.GetPagination(); p != nil && p.GetPage() > 0 {
		page = int(p.GetPage())
	}

	reqs, total, err := l.svcCtx.FriendRequestRepo.ListSent(l.ctx, userID, offset, limit)
	if err != nil {
		return nil, err
	}

	userIDs := make([]int64, 0, len(reqs))
	for _, r := range reqs {
		userIDs = append(userIDs, r.ToUserID)
	}
	userMap, _ := l.svcCtx.UserClient.BatchGetUserInfo(l.ctx, userIDs)

	items := make([]*friend.FriendRequest, 0, len(reqs))
	for _, r := range reqs {
		u := userMap[r.ToUserID]
		items = append(items, &friend.FriendRequest{
			RequestId:    r.ID,
			FromUserId:   r.FromUserID,
			ToUserId:     r.ToUserID,
			Message:      r.Message,
			Status:       statusToString(r.Status),
			CreatedAt:    r.CreatedAt.Unix(),
			UpdatedAt:    r.UpdatedAt.Unix(),
			FromUsername: usernameFromUser(u),
			FromAvatar:   avatarFromUser(u),
		})
	}

	return &friend.ListRequestsResp{
		Requests:   items,
		Pagination: paginationResp(page, limit, total),
	}, nil
}
