package messageservicelogic

import (
	"context"
	"encoding/json"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SendBotReplyLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendBotReplyLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendBotReplyLogic {
	return &SendBotReplyLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *SendBotReplyLogic) SendBotReply(in *message.SendBotReplyReq) (*message.SendBotReplyResp, error) {
	if in == nil || validateIdentities(in.ConversationId, in.BotId, in.BindingId) != nil || (in.ReplyToId != nil && identity.Validate(*in.ReplyToId) != nil) || (in.RequesterId != nil && identity.Validate(*in.RequesterId) != nil) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid bot sending identity")
	}
	// Bot credentials and domain calls never borrow a user's access identity.
	if callerUserID(l.ctx) != "" {
		return nil, errors.New(errors.CodeForbidden, "bot output cannot use a user identity")
	}
	if err := validateBotMetadata(in.RawPayload); err != nil {
		return nil, errors.New(errors.CodeInvalidParam, err.Error())
	}
	kind := in.Type
	if kind == message.MessageType_MESSAGE_TYPE_UNSPECIFIED {
		kind = message.MessageType_MESSAGE_TYPE_TEXT
	}
	var content model.JSONContent
	switch kind {
	case message.MessageType_MESSAGE_TYPE_TEXT:
		if in.File != nil || in.Result != nil || in.Text == "" {
			return nil, errors.New(errors.CodeInvalidParam, "invalid bot text")
		}
		content = model.TextContent{Text: in.Text}.ToJSONContent()
	case message.MessageType_MESSAGE_TYPE_FILE:
		if in.File == nil || identity.Validate(in.File.FileId) != nil || in.Text != "" || in.Result != nil {
			return nil, errors.New(errors.CodeInvalidParam, "invalid bot file")
		}
	case message.MessageType_MESSAGE_TYPE_RESULT:
		if in.Result == nil || in.Result.Type == "" || !json.Valid([]byte(in.Result.Data)) || in.File != nil || in.Text != "" {
			return nil, errors.New(errors.CodeInvalidParam, "invalid bot result")
		}
		content = model.CustomContent{Type: in.Result.Type, Data: in.Result.Data}.ToJSONContent()
	default:
		return nil, errors.New(errors.CodeInvalidParam, "unsupported bot content type")
	}
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	msg := &model.Message{ID: id, ConvID: in.ConversationId, SenderID: &in.BotId, SenderType: "bot", BindingID: &in.BindingId, RequesterID: in.RequesterId, RawPayload: in.RawPayload, MsgType: int32(kind), Content: content, ReplyToMsgID: in.ReplyToId, Status: model.MessageStatusNormal, EditHistory: model.JSONArray{}, CreatedAt: now, UpdatedAt: now}
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockBotBinding(l.ctx, tx, in.ConversationId, in.BotId, in.BindingId); err != nil {
			return err
		}
		// Resolve display metadata locally; the send hot path never calls Bot control.
		bot, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, in.BotId)
		if err != nil || bot.Status != "active" {
			return errors.New(errors.CodeForbidden, "bot is unavailable")
		}
		msg.SenderName, msg.SenderAvatar = bot.Name, bot.Avatar
		if in.RequesterId != nil {
			var count int64
			if err := tx.Model(&model.ConversationMember{}).Where("conv_id = ? AND user_id = ?", in.ConversationId, *in.RequesterId).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrNotMember
			}
		}
		if kind == message.MessageType_MESSAGE_TYPE_FILE {
			// Only a public, confirmed object can be disclosed without a separate resource
			// grant. Neither Bot ownership nor the requester authorizes private files.
			var file struct {
				ID       string
				Name     string
				Size     int64
				Ext      string
				MimeType string
			}
			if err := tx.Table("file.files").Select("id, name, size, ext, mime_type").Where("id = ? AND access = 3", in.File.FileId).Take(&file).Error; err != nil {
				return errors.New(errors.CodeForbidden, "file is not publicly shareable")
			}
			msg.Content = model.FileContent{FileID: file.ID, Name: file.Name, Size: file.Size, Ext: file.Ext, MimeType: file.MimeType}.ToJSONContent()
		}
		if err := validateReplyTarget(l.ctx, l.svcCtx, tx, in.ConversationId, "", in.ReplyToId); err != nil {
			return err
		}
		return persistMessage(l.ctx, l.svcCtx, tx, msg, msg.SenderName)
	})
	if err != nil {
		return nil, err
	}
	l.Infof("bot message committed: message_id=%s bot_id=%s binding_id=%s", id, in.BotId, in.BindingId)
	return &message.SendBotReplyResp{MessageId: id, Seq: msg.Seq, CreatedAt: now.Unix()}, nil
}

// All membership changes take the same conversation lock. Checking the exact
// binding ID prevents an old executor from acquiring a replacement lifecycle.
func lockBotBinding(ctx context.Context, tx *gorm.DB, convID, botID, bindingID string) (*model.ConvBot, error) {
	var conv model.Conversation
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", convID).Take(&conv).Error; err != nil {
		return nil, err
	}
	var binding model.ConvBot
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND conv_id = ? AND bot_id = ? AND revoked_at IS NULL", bindingID, convID, botID).
		Where("EXISTS (SELECT 1 FROM bot.bots b WHERE b.id = conv_bots.bot_id AND b.status = 'active')").Take(&binding).Error; err != nil {
		return nil, ErrBotNotInConversation
	}
	return &binding, nil
}
