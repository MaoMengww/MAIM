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
	userID := userIDFromContext(l.ctx)
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

	if err := l.svcCtx.FriendRequestRepo.UpdateStatus(l.ctx, req.ID, model.FriendRequestStatusAccepted); err != nil {
		return nil, grpcError(err)
	}
	if err := l.svcCtx.FriendRepo.CreatePair(l.ctx, req.FromUserID, req.ToUserID, 0, func() int64 { id, _ := l.svcCtx.Snowflake.Generate(); return id }); err != nil {
		return nil, grpcError(err)
	}

	l.Infof("friend request accepted: requester_id=%d addressee_id=%d", req.FromUserID, req.ToUserID)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
