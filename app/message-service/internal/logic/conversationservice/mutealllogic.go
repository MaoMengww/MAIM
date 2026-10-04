package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type MuteAllLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewMuteAllLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MuteAllLogic {
	return &MuteAllLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *MuteAllLogic) MuteAll(in *conversation.MuteAllReq) (*common.BaseResponse, error) {
	if err := requireRole(l.ctx, l.svcCtx.ConversationRepo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	if err := l.svcCtx.ConversationRepo.UpdateConversationMutedAll(l.ctx, in.ConversationId, true); err != nil {
		l.Logger.Errorf("mute all failed: %v", err)
		return nil, err
	}

	// 发送系统消息
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "conversation.muted_all", "开启了全员禁言", nil)

	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
