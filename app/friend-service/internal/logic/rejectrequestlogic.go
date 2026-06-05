package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/model"
	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type RejectRequestLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRejectRequestLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RejectRequestLogic {
	return &RejectRequestLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *RejectRequestLogic) RejectRequest(in *friend.RejectRequestReq) (*common.BaseResponse, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	req, err := l.svcCtx.FriendRequestRepo.GetByID(l.ctx, in.GetRequestId())
	if err != nil {
		return nil, grpcError(ErrRequestNotFound)
	}
	if req.ToUserID != userID {
		return nil, grpcError(ErrNotRecipient)
	}
	if req.Status != model.FriendRequestStatusPending {
		return nil, grpcError(ErrRequestAlreadyHandled)
	}

	if err := l.svcCtx.FriendRequestRepo.UpdateStatus(l.ctx, req.ID, model.FriendRequestStatusRejected); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("friend request rejected: requester_id=%d addressee_id=%d", req.FromUserID, req.ToUserID)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
