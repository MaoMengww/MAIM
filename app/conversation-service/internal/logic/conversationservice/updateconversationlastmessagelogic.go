package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type UpdateConversationLastMessageLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUpdateConversationLastMessageLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateConversationLastMessageLogic {
	return &UpdateConversationLastMessageLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UpdateConversationLastMessageLogic) UpdateConversationLastMessage(in *conversation.UpdateConversationLastMessageReq) (*common.BaseResponse, error) {
	if err := l.svcCtx.Repo.UpdateConversationLastMessage(l.ctx, in.ConversationId, in.LastMessageId, in.LastMessagePreview, in.MaxSeq); err != nil {
		l.Logger.Errorf("update conversation last message failed: %v", err)
		return nil, err
	}
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
