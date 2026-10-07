package friend

import (
	"context"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/pb/common"

	"github.com/zeromicro/go-zero/core/logx"
)

type AcceptRequestLogic struct {
	ctx    context.Context
	svcCtx *Context
	logx.Logger
}

func NewAcceptRequestLogic(ctx context.Context, svcCtx *Context) *AcceptRequestLogic {
	return &AcceptRequestLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *AcceptRequestLogic) AcceptRequest(in *userpb.AcceptRequestReq) (*common.BaseResponse, error) {
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

	if blocked, err := l.svcCtx.BlockRepo.IsBlocked(l.ctx, req.FromUserID, req.ToUserID); err != nil {
		return nil, grpcError(err)
	} else if blocked {
		return nil, grpcError(ErrBlocked)
	}
	if err := l.svcCtx.FriendRequestRepo.Accept(l.ctx, req); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("friend request accepted: requester_id=%s addressee_id=%s", req.FromUserID, req.ToUserID)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
