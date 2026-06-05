package conversationservice

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

// BotResolveLimit is the number of members to fetch when resolving bot info.
const BotResolveLimit = 10

type GetConversationLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetConversationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetConversationLogic {
	return &GetConversationLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetConversationLogic) GetConversation(in *conversation.GetConversationReq) (*conversation.GetConversationResp, error) {
	conv, err := l.svcCtx.Repo.GetConversation(l.ctx, in.ConversationId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrConvNotFound
		}
		l.Logger.Errorf("get conversation failed: %v", err)
		return nil, err
	}

	var lastReadSeq int64
	var unread int32
	var muted, pinned bool
	settings, err := l.svcCtx.Repo.GetSettings(l.ctx, in.ConversationId, in.UserId)
	if err == nil {
		muted = settings.IsMuted
		pinned = settings.IsPinned
	}

	pbConv := toProtoConv(conv, lastReadSeq, unread, muted, pinned)

	// Resolve peer info for private conversations.
	if conv.Type == 1 {
		members, err := l.svcCtx.Repo.GetMembers(l.ctx, conv.ID, 0, BotResolveLimit)
		if err == nil {
			for _, m := range members {
				if m.UserID != in.UserId {
					if bot, err := l.svcCtx.Repo.GetBot(l.ctx, m.UserID); err == nil {
						pbConv.Name = bot.Name
						pbConv.Avatar = bot.Avatar
					} else if l.svcCtx.UserClient != nil {
						if user, err := l.svcCtx.UserClient.GetUserInfo(l.ctx, m.UserID); err == nil {
							pbConv.Name = user.Username
							pbConv.Avatar = user.Avatar
						}
					}
					break
				}
			}
		}
	}

	l.Infof("conversation fetched: conv_id=%d", in.ConversationId)
	return &conversation.GetConversationResp{
		Conversation: pbConv,
	}, nil
}
