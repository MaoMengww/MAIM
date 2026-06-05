package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListPendingRequestsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListPendingRequestsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListPendingRequestsLogic {
	return &ListPendingRequestsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListPendingRequestsLogic) ListPendingRequests(in *friend.ListPendingRequestsReq) (*friend.ListRequestsResp, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
		return nil, ErrUnauthenticated
	}

	offset, limit := paginationParams(in.GetPagination())

	page := 1
	if p := in.GetPagination(); p != nil && p.GetPage() > 0 {
		page = int(p.GetPage())
	}

	reqs, total, err := l.svcCtx.FriendRequestRepo.ListPending(l.ctx, userID, offset, limit)
	if err != nil {
		return nil, err
	}

	userIDs := make([]int64, 0, len(reqs))
	for _, r := range reqs {
		userIDs = append(userIDs, r.FromUserID)
	}
	userMap, _ := l.svcCtx.UserClient.BatchGetUserInfo(l.ctx, userIDs)

	items := make([]*friend.FriendRequest, 0, len(reqs))
	for _, r := range reqs {
		u := userMap[r.FromUserID]
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
