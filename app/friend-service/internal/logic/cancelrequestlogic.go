package logic

import (
	"context"

	"github.com/maomeng/aim/app/friend-service/internal/model"
	"github.com/maomeng/aim/app/friend-service/internal/svc"
	"github.com/maomeng/aim/app/friend-service/pb/friend"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type CancelRequestLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewCancelRequestLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CancelRequestLogic {
	return &CancelRequestLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *CancelRequestLogic) CancelRequest(in *friend.CancelRequestReq) (*common.BaseResponse, error) {
	userID := svc.UserIDFromCtx(l.ctx)
	if userID == 0 {
		return nil, grpcError(ErrUnauthenticated)
	}

	req, err := l.svcCtx.FriendRequestRepo.GetByID(l.ctx, in.GetRequestId())
	if err != nil {
		return nil, grpcError(ErrRequestNotFound)
	}
	if req.FromUserID != userID {
		return nil, grpcError(ErrNotSender)
	}
	if req.Status != model.FriendRequestStatusPending {
		return nil, grpcError(ErrRequestAlreadyHandled)
	}

	if err := l.svcCtx.FriendRequestRepo.UpdateStatus(l.ctx, req.ID, model.FriendRequestStatusCancelled); err != nil {
		return nil, grpcError(err)
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
