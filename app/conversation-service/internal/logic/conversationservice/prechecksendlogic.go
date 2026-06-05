package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"

	"github.com/zeromicro/go-zero/core/logx"
)

type PreCheckSendLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewPreCheckSendLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PreCheckSendLogic {
	return &PreCheckSendLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *PreCheckSendLogic) PreCheckSend(in *conversation.PreCheckSendReq) (*conversation.PreCheckSendResp, error) {
	resp := &conversation.PreCheckSendResp{}

	// 1. 成员身份（关键检查：失败则拒收）
	isMember, err := l.svcCtx.Repo.IsMember(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		l.Errorf("check member failed for conv=%d user=%d: %v", in.ConversationId, in.UserId, err)
		return nil, err
	}
	resp.IsMember = isMember
	if !isMember {
		return resp, nil
	}

	// 2. 会话信息（类型 + 全员禁言）
	conv, err := l.svcCtx.Repo.GetConversation(l.ctx, in.ConversationId)
	if err != nil {
		l.Errorf("get conversation failed for conv=%d: %v", in.ConversationId, err)
		// 非关键，继续
	} else {
		resp.IsMutedAll = conv.IsMutedAll
		resp.ConvType = conversation.ConversationType(conv.Type)
	}

	// 3. 成员禁言状态
	member, err := l.svcCtx.Repo.GetMember(l.ctx, in.ConversationId, in.UserId)
	if err != nil {
		l.Errorf("get member failed for conv=%d user=%d: %v", in.ConversationId, in.UserId, err)
	} else if member != nil {
		resp.IsMuted = member.IsMuted
		resp.MuteUntil = member.MuteUntil
	}

	// 4. 私聊：获取对方成员 ID（群聊跳过拉黑检查）
	if resp.ConvType == conversation.ConversationType_CONVERSATION_TYPE_PRIVATE {
		members, err := l.svcCtx.Repo.GetMembers(l.ctx, in.ConversationId, 0, 2)
		if err != nil {
			l.Errorf("get members for block check failed conv=%d: %v", in.ConversationId, err)
		} else {
			for _, m := range members {
				if m.UserID != in.UserId {
					resp.MemberIds = append(resp.MemberIds, m.UserID)
				}
			}
		}
	}

	return resp, nil
}
