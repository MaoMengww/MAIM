package friend

import (
	"context"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type CancelRequestLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewCancelRequestLogic(ctx context.Context, svcCtx *Context) *CancelRequestLogic {
	return &CancelRequestLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *CancelRequestLogic) CancelRequest(in *userpb.CancelRequestReq) (*common.BaseResponse, error) {
	if err := validateCaller(l.ctx, in.GetUserId(), in.GetRequestId()); err != nil {
		return nil, err
	}
	userID := userIDFromContext(l.ctx)
	if userID == "" {
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
