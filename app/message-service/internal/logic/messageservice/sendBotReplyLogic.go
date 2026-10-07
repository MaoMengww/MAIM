package messageservicelogic

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"

	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type SendBotReplyLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendBotReplyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendBotReplyLogic {
	return &SendBotReplyLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendBotReplyLogic) SendBotReply(in *message.SendBotReplyReq) (*message.SendBotReplyResp, error) {
	if in == nil || validateIdentities(in.ConversationId, in.BotId) != nil || (in.ReplyToId != nil && validateIdentities(*in.ReplyToId) != nil) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid bot reply identity")
	}
	msgID, err := identity.New()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate msg id failed", err)
	}
	now := time.Now()
	content := extractBotContent(in)

	// 只读 Bot 展示投影，不依赖控制面 RPC (ADR-0007)。
	if bot, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, in.BotId); err == nil {
		content.BotName = bot.Name
		content.BotAvatar = bot.Avatar
	}

	msg := &model.Message{
		ID:           msgID,
		ConvID:       in.ConversationId,
		SenderID:     &in.BotId,
		MsgType:      model.MsgTypeBot,
		SenderType:   "bot",
		Content:      content.ToJSONContent(),
		ReplyToMsgID: in.ReplyToId,
		Status:       model.MessageStatusNormal,
		EditHistory:  model.JSONArray{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 事务：锁会话 + 成员校验 + seq + 消息 + outbox + 最新消息（原子提交）
	var seq int64
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		perm, err := l.svcCtx.ConversationRepo.CheckSendPermission(l.ctx, tx, in.ConversationId, in.BotId, model.MemberTypeBot)
		if err != nil {
			return err
		}
		if !perm.IsMember {
			return ErrBotNotInConversation
		}

		if err := validateReplyTarget(l.ctx, l.svcCtx, tx, in.ConversationId, "", in.ReplyToId); err != nil {
			return err
		}
		if err := persistMessage(l.ctx, l.svcCtx, tx, msg, content.BotName); err != nil {
			return err
		}
		seq = msg.Seq
		return nil
	})
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New(errors.CodeNotFound, "conversation not found")
		}
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "insert bot message failed", err)
	}

	l.Infof("bot reply sent: msg_id=%s bot_id=%s", msgID, in.BotId)

	return &message.SendBotReplyResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}
