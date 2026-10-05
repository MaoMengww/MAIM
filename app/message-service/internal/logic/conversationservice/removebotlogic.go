package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type RemoveBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRemoveBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RemoveBotLogic {
	return &RemoveBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *RemoveBotLogic) RemoveBot(in *conversation.RemoveBotReq) (*common.BaseResponse, error) {
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
			return err
		}
		member, err := r.GetMember(l.ctx, conv.ID, in.BotId)
		if err != nil {
			return err
		}
		if member.MemberType != model.MemberTypeBot {
			return ErrMemberRemoveFailed
		}
		if err := r.RemoveBotWithMember(l.ctx, conv.ID, in.BotId); err != nil {
			return err
		}
		if err := r.IncrementMemberCount(l.ctx, conv.ID, -1); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "bot.removed", "机器人离开了群聊", nil)
	l.Infof("bot removed: conv_id=%d bot_id=%d", in.ConversationId, in.BotId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
