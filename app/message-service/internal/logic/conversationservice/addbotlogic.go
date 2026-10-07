package conversationservice

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"github.com/maomeng/aim/pkg/identity"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type AddBotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewAddBotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AddBotLogic {
	return &AddBotLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *AddBotLogic) AddBot(in *conversation.AddBotReq) (*common.BaseResponse, error) {

	if err := validateRequest(l.ctx, in.OperatorId, in.ConversationId, in.BotId); err != nil {
		return nil, err
	}
	_, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, in.BotId)
	if err != nil {
		l.Errorf("get bot failed: bot_id=%s, err=%v", in.BotId, err)
		return nil, ErrMemberAddFailed
	}

	cbID, err := identity.New()
	if err != nil {
		return nil, fmt.Errorf("generate conv bot id failed: %w", err)
	}
	cb := &model.ConvBot{
		ID:      cbID,
		ConvID:  in.ConversationId,
		BotID:   in.BotId,
		AddedBy: in.OperatorId,
	}
	if in.BotSettings != "" {
		var settings map[string]any
		if err := json.Unmarshal([]byte(in.BotSettings), &settings); err == nil {
			cb.BotSettings = settings
		}
	}

	memberID, err := identity.New()
	if err != nil {
		return nil, fmt.Errorf("generate member id failed: %w", err)
	}
	member := &model.ConversationMember{
		ID:         memberID,
		ConvID:     in.ConversationId,
		MemberType: model.MemberTypeBot,
		BotID:      &in.BotId,
		Role:       int32(conversation.MemberRole_MEMBER_ROLE_MEMBER),
		JoinedAt:   time.Now(),
	}

	err = withLockedConversation(l.ctx, l.svcCtx, in.ConversationId, func(tx *gorm.DB, r *repo.ConversationRepo, conv *model.Conversation) error {
		if err := requireRole(l.ctx, r, conv.ID, in.OperatorId, adminRole); err != nil {
			return err
		}
		added, _, err := r.AddMembersWithinLimit(l.ctx, conv.ID, []model.ConversationMember{*member}, l.svcCtx.Config.Conv.MaxMemberCount)
		if err != nil {
			return err
		}
		if len(added) == 0 {
			return ErrMemberAddFailed
		}
		if err := r.AddBot(l.ctx, cb); err != nil {
			return err
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil)
	})
	if errors.Is(err, repo.ErrMemberLimitReached) {
		return nil, ErrConvMaxMembers
	}
	if err != nil {
		return nil, err
	}
	// Notify bot-service via Kafka
	if l.svcCtx.BotEventProducer != nil && l.svcCtx.BotEventProducer.SyncProducer != nil {
		eventData, _ := json.Marshal(map[string]any{
			"event_type": "bot.added_to_conv",
			"conv_id":    in.ConversationId,
			"bot_id":     in.BotId,
			"added_by":   in.OperatorId,
		})
		if err := l.svcCtx.BotEventProducer.Send(l.ctx, "", eventData); err != nil {
			l.Logger.Errorf("send bot.added event failed: conv_id=%s, bot_id=%s, err=%v",
				in.ConversationId, in.BotId, err)
		}
	}
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "bot.joined", "机器人加入了群聊", nil)
	l.Infof("bot added: conv_id=%s bot_id=%s", in.ConversationId, in.BotId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
