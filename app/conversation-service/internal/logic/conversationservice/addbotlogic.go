package conversationservice

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
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
	bot, err := l.svcCtx.Repo.GetBot(l.ctx, in.BotId)
	if err != nil {
		l.Errorf("get bot failed: bot_id=%d, err=%v", in.BotId, err)
		return nil, ErrMemberAddFailed
	}

	cb := &model.ConvBot{
		ID:               l.svcCtx.Snowflake.Generate(),
		ConvID:           in.ConversationId,
		BotID:            in.BotId,
		AddedBy:          in.OperatorId,
		ResponseTriggers: in.ResponseTriggers,
	}
	if len(cb.ResponseTriggers) == 0 {
		cb.ResponseTriggers = []string{"mention"}
	}
	if in.BotSettings != "" {
		var settings map[string]any
		if err := json.Unmarshal([]byte(in.BotSettings), &settings); err == nil {
			cb.BotSettings = settings
		}
	}

	member := &model.ConversationMember{
		ID:         l.svcCtx.Snowflake.Generate(),
		ConvID:     in.ConversationId,
		UserID:     bot.ID,
		MemberType: model.MemberTypeBot,
		BotID:      in.BotId,
		Role:       int32(conversation.MemberRole_MEMBER_ROLE_MEMBER),
		JoinedAt:   time.Now(),
	}

	if err := l.svcCtx.Repo.AddBotWithMember(l.ctx, cb, member); err != nil {
		l.Errorf("add bot with member failed: conv_id=%d, bot_id=%d, err=%v", in.ConversationId, in.BotId, err)
		return nil, ErrMemberAddFailed
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
			l.Logger.Errorf("send bot.added event failed: conv_id=%d, bot_id=%d, err=%v",
				in.ConversationId, in.BotId, err)
		}
	}
	emitSystemMessage(l.ctx, l.svcCtx, in.ConversationId, in.OperatorId, "bot.joined", "机器人加入了群聊", nil)
	l.Infof("bot added: conv_id=%d bot_id=%d", in.ConversationId, in.BotId)
	return &common.BaseResponse{Code: 0, Message: "ok"}, nil
}
