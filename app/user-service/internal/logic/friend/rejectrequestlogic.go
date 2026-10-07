package friend

import (
	"context"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type RejectRequestLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewRejectRequestLogic(ctx context.Context, svcCtx *Context) *RejectRequestLogic {
	return &RejectRequestLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *RejectRequestLogic) RejectRequest(in *userpb.RejectRequestReq) (*common.BaseResponse, error) {
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
	if req.ToUserID != userID {
		return nil, grpcError(ErrNotRecipient)
	}
	if req.Status != model.FriendRequestStatusPending {
		return nil, grpcError(ErrRequestAlreadyHandled)
	}

	if err := l.svcCtx.FriendRequestRepo.UpdateStatus(l.ctx, req.ID, model.FriendRequestStatusRejected); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("friend request rejected: requester_id=%s addressee_id=%s", req.FromUserID, req.ToUserID)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
