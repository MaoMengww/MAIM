package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type UnmuteAllLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewUnmuteAllLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UnmuteAllLogic {
	return &UnmuteAllLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *UnmuteAllLogic) UnmuteAll(in *conversation.UnmuteAllReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.ConversationRepo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	if err := l.svcCtx.ConversationRepo.UpdateConversationMutedAll(l.ctx, in.ConversationId, false); err != nil {
		l.Logger.Errorf("unmute all failed: %v", err)
		return nil, err
	}

	// 发送系统消息
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "conversation.unmuted_all", "关闭了全员禁言", nil)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
