package friend

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListPendingRequestsLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewListPendingRequestsLogic(ctx context.Context, svcCtx *Context) *ListPendingRequestsLogic {
	return &ListPendingRequestsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListPendingRequestsLogic) ListPendingRequests(in *userpb.ListPendingRequestsReq) (*userpb.ListRequestsResp, error) {
	userID := userIDFromContext(l.ctx)
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
	userMap, _ := l.svcCtx.batchGetUserInfo(l.ctx, userIDs)

	items := make([]*userpb.FriendRequest, 0, len(reqs))
	for _, r := range reqs {
		u := userMap[r.FromUserID]
		items = append(items, &userpb.FriendRequest{
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

	return &userpb.ListRequestsResp{
		Requests:   items,
		Pagination: paginationResp(page, limit, total),
	}, nil
}
