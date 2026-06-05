package conversationservice

import (
	"context"
	"errors"
	"time"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type UpdateConversationLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateConversationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateConversationLogic {
	return &UpdateConversationLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateConversationLogic) UpdateConversation(in *conversation.UpdateConversationReq) (*common.BaseResponse, error) {
	conv, err := l.svcCtx.Repo.GetConversation(l.ctx, in.ConversationId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrConvNotFound
		}
		l.Logger.Errorf("get conversation failed: %v", err)
		return nil, err
	}

	// Only group conversations can be updated
	if conv.Type != int32(conversation.ConversationType_CONVERSATION_TYPE_GROUP) {
		return nil, status.Error(codes.PermissionDenied, "only group conversations can be updated")
	}
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.UserId, adminRole); err != nil {
		return nil, err
	}

	if in.Name != nil {
		conv.Name = in.GetName()
	}
	if in.Avatar != nil {
		conv.Avatar = in.GetAvatar()
	}
	if in.Background != nil {
		conv.Background = in.GetBackground()
	}
	conv.UpdatedAt = time.Now()

	if err := l.svcCtx.Repo.UpdateConversation(l.ctx, conv); err != nil {
		l.Logger.Errorf("update conversation failed: %v", err)
		return nil, err
	}

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
