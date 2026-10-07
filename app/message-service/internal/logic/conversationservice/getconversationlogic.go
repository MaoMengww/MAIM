package conversationservice

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
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

	if err := validateRequest(l.ctx, in.UserId, in.ConversationId); err != nil {
		return nil, err
	}
	{
		isMember, err := l.svcCtx.ConversationRepo.IsMember(l.ctx, in.ConversationId, in.UserId)
		if err != nil {
			return nil, err
		}
		if !isMember {
			return nil, ErrNotMember
		}
	}
	conv, err := l.svcCtx.ConversationRepo.GetConversation(l.ctx, in.ConversationId)
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
	settings, err := l.svcCtx.ConversationRepo.GetSettings(l.ctx, in.ConversationId, in.UserId)
	if err == nil {
		muted = settings.IsMuted
		pinned = settings.IsPinned
	}
	seq, err := l.svcCtx.ConversationRepo.GetReadSeq(l.ctx, in.ConversationId, in.UserId)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		l.Logger.Errorf("get read sequence failed for conv=%s user=%s: %v", in.ConversationId, in.UserId, err)
		return nil, err
	}
	if seq != nil {
		lastReadSeq = seq.LastReadSeq
	}
	{
		count, err := l.svcCtx.ConversationRepo.UnreadCount(l.ctx, in.ConversationId, in.UserId)
		if err != nil {
			l.Logger.Errorf("unread count failed for conv=%s user=%s: %v", in.ConversationId, in.UserId, err)
			return nil, err
		}
		unread = count
	}

	projected := []model.Conversation{*conv}
	if err := l.svcCtx.MessageRepo.ProjectConversationPreviews(l.ctx, in.UserId, projected); err != nil {
		return nil, err
	}
	conv = &projected[0]
	pbConv := toProtoConv(conv, lastReadSeq, unread, muted, pinned)

	NewCreateConversationLogic(l.ctx, l.svcCtx).resolvePrivatePeerInfo(pbConv, in.UserId)

	l.Infof("conversation fetched: conv_id=%s", in.ConversationId)
	return &conversation.GetConversationResp{
		Conversation: pbConv,
	}, nil
}
