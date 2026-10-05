package conversationservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
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
	memberRole := int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)
	members := make([]model.ConversationMember, 0, len(in.UserIds))

	for _, uid := range in.UserIds {
		memberID, err := l.svcCtx.Snowflake.Generate()
		if err != nil {
			return nil, fmt.Errorf("generate member id failed: %w", err)
		}
		members = append(members, model.ConversationMember{
			ID:         memberID,
			ConvID:     in.ConversationId,
			UserID:     uid,
			MemberType: model.MemberTypeUser,
			Role:       memberRole,
			JoinedAt:   now,
		})
	}

	var added, failed []int64
	err := withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
			return err
		}
		var err error
		added, failed, err = r.AddMembersWithinLimit(l.ctx, conv.ID, members, l.svcCtx.Config.Conv.MaxMemberCount)
		if err != nil || len(added) == 0 {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if errors.Is(err, repo.ErrMemberLimitReached) {
		return nil, ErrConvMaxMembers
	}
	if err != nil {
		return nil, fmt.Errorf("add members failed: %w", err)
	}

	if len(added) > 0 {
		emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, consts.ConvActionMemberJoined, "成员加入了群聊", added)
	}

	l.Infof("members added: conv_id=%d count=%d", in.ConversationId, len(added))
	return &conversation.AddMembersResp{
		AddedUserIds:  added,
		FailedUserIds: failed,
	}, nil
}
