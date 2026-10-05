package messageservicelogic

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

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
	msgID, err := l.svcCtx.Snowflake.Generate()
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
		SenderID:     in.BotId,
		MsgType:      model.MsgTypeBot,
		SenderType:   "bot",
		Content:      content.ToJSONContent(),
		ReplyToMsgID: in.GetReplyToId(),
		Status:       model.MessageStatusNormal,
		EditHistory:  model.JSONArray{},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// 事务外预取 reply_to（涉及 DB 查询，不放在事务内）
	replyToMap := map[string]any{}
	if replyToID := in.GetReplyToId(); replyToID != 0 {
		replyToMap = buildReplyToMap(l.ctx, l.svcCtx, replyToID)
	}
	previewText := extractTextPreview(int32(model.MsgTypeBot), model.JSONContent{"text": in.Text})

	// 事务：锁会话 + 成员校验 + seq + 消息 + outbox + 最新消息（原子提交）
	var seq int64
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		perm, err := l.svcCtx.ConversationRepo.CheckSendPermission(l.ctx, tx, in.ConversationId, in.BotId)
		if err != nil {
			return err
		}
		if !perm.IsMember {
			return ErrBotNotInConversation
		}

		seq, err = l.svcCtx.SequenceRepo.NextSeq(l.ctx, tx, in.ConversationId)
		if err != nil {
			return err
		}
		msg.Seq = seq

		if err := tx.Create(msg).Error; err != nil {
			return err
		}

		payload := buildMessageCreatedPayload(msg, content.BotName)
		if len(replyToMap) > 0 {
			payload["reply_to"] = replyToMap
		}
		if err := l.svcCtx.PublishInboxChange(l.ctx, tx, payload); err != nil {
			return err
		}

		// 同一事务内推进会话的最新消息、最大 seq 与 updated_at
		return l.svcCtx.ConversationRepo.TouchLastMessage(l.ctx, tx, in.ConversationId, msgID, seq, previewText)
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

	l.Infof("bot reply sent: msg_id=%d bot_id=%d", msgID, in.BotId)

	return &message.SendBotReplyResp{
		MessageId: msgID,
		Seq:       seq,
		CreatedAt: now.Unix(),
	}, nil
}
