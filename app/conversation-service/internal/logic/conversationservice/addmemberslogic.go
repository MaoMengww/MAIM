package conversationservice

import (
	"context"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/zeromicro/go-zero/core/logx"
)

type AddMembersLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewAddMembersLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AddMembersLogic {
	return &AddMembersLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *AddMembersLogic) AddMembers(in *conversation.AddMembersReq) (*conversation.AddMembersResp, error) {
	now := time.Now()
	if err := requireRole(l.ctx, l.svcCtx.Repo, in.ConversationId, in.OperatorId, adminRole); err != nil {
		return nil, err
	}
	memberRole := int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)
	var added, failed []int64

	for _, uid := range in.UserIds {
		isMember, err := l.svcCtx.Repo.IsMember(l.ctx, in.ConversationId, uid)
		if err != nil {
			l.Logger.Errorf("check member failed: conv=%d, uid=%d, err=%v", in.ConversationId, uid, err)
		}
		if isMember {
			failed = append(failed, uid)
			continue
		}
		memberID, err := l.svcCtx.Snowflake.Generate()
		if err != nil {
			return nil, fmt.Errorf("generate member id failed: %w", err)
		}
		m := model.ConversationMember{
			ID:       memberID,
			ConvID:   in.ConversationId,
			UserID:   uid,
			Role:     memberRole,
			JoinedAt: now,
		}
		if err := l.svcCtx.Repo.AddMember(l.ctx, &m); err != nil {
			failed = append(failed, uid)
			continue
		}
		added = append(added, uid)
	}

	if len(added) > 0 {
		if err := l.svcCtx.Repo.IncrementMemberCount(l.ctx, in.ConversationId, len(added)); err != nil {
			l.Logger.Errorf("increment member count failed: conv=%d, delta=%d, err=%v", in.ConversationId, len(added), err)
		}
		emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, consts.ConvActionMemberJoined, "成员加入了群聊", added)
	}

	l.Infof("members added: conv_id=%d count=%d", in.ConversationId, len(added))
	return &conversation.AddMembersResp{
		AddedUserIds:  added,
		FailedUserIds: failed,
	}, nil
}
