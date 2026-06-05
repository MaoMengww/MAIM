package conversationservice

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type GetMuteStatusLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetMuteStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetMuteStatusLogic {
	return &GetMuteStatusLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetMuteStatusLogic) GetMuteStatus(in *conversation.GetMuteStatusReq) (*conversation.GetMuteStatusResp, error) {
	member, err := l.svcCtx.Repo.GetMember(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMemberNotFound
		}
		l.Logger.Errorf("get member failed: %v", err)
		return nil, err
	}

	conv, err := l.svcCtx.Repo.GetConversation(l.ctx, in.ConversationId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrConvNotFound
		}
		l.Logger.Errorf("get conversation failed: %v", err)
		return nil, err
	}

	return &conversation.GetMuteStatusResp{
		IsMuted:    member.IsMuted,
		IsMutedAll: conv.IsMutedAll,
		MuteUntil:  member.MuteUntil,
	}, nil
}
