package messageservicelogic

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"
	"unicode/utf8"

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

// The publication receipt is immutable even if the message is later edited or
// hidden. Its content uses the same semantic JSON fingerprint as user sends.
type botOutputSubmission struct {
	BotID              string            `gorm:"primaryKey;type:uuid"`
	BindingID          string            `gorm:"primaryKey;type:uuid"`
	OutputKey          string            `gorm:"primaryKey"`
	ConvID             string            `gorm:"type:uuid"`
	ContentFingerprint model.JSONContent `gorm:"type:jsonb"`
	MessageID          string            `gorm:"type:uuid"`
	Seq                int64
	CreatedAt          time.Time
}

func (botOutputSubmission) TableName() string { return "bot_output_submissions" }

func (l *SendBotReplyLogic) SendBotReply(in *message.SendBotReplyReq) (*message.SendBotReplyResp, error) {
	if in == nil || validateIdentities(in.ConversationId, in.BotId, in.BindingId) != nil || (in.ReplyToId != nil && identity.Validate(*in.ReplyToId) != nil) || (in.RequesterId != nil && identity.Validate(*in.RequesterId) != nil) || (in.ControllerId != nil && identity.Validate(*in.ControllerId) != nil) || (in.RunId != nil && identity.Validate(*in.RunId) != nil) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid bot sending identity")
	}
	// Bot credentials and domain calls never borrow a user's access identity.
	if callerUserID(l.ctx) != "" {
		return nil, errors.New(errors.CodeForbidden, "bot output cannot use a user identity")
	}
	if in.OutputKey != nil && (*in.OutputKey == "" || len(*in.OutputKey) > 512 || !utf8.ValidString(*in.OutputKey) || strings.ContainsRune(*in.OutputKey, 0)) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid bot output key")
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
	msg := &model.Message{ConvID: in.ConversationId, SenderID: &in.BotId, SenderType: "bot", BindingID: &in.BindingId, RequesterID: in.RequesterId, RawPayload: in.RawPayload, MsgType: int32(kind), Content: content, ReplyToMsgID: in.ReplyToId, Status: model.MessageStatusNormal, EditHistory: model.JSONArray{}}
	var receipt *botOutputSubmission
	created := false
	err := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockBotBinding(l.ctx, tx, in.ConversationId, in.BotId, in.BindingId); err != nil {
			return err
		}
		// Resolve display metadata locally; the send hot path never calls Bot control.
		var bot struct {
			Name   string
			Avatar string
			Status string
		}
		if err := tx.Table("bot.bots").Select("name, avatar, status").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", in.BotId).Take(&bot).Error; err != nil || bot.Status != "active" {
			return errors.New(errors.CodeForbidden, "bot is unavailable")
		}
		msg.SenderName, msg.SenderAvatar = bot.Name, bot.Avatar
		controller := in.GetControllerId()
		if controller == "" {
			controller = in.GetRequesterId()
		}
		if in.RunId != nil {
			// Runtime source RPCs finish before taking the conversation barrier.
			var run struct {
				ControllerID                string
				ControllerMemberID          *string
				Status                      string
				AccessRevoked               bool
				BotAuthorizationVersion     int64
				BindingAuthorizationVersion int64
			}
			if err := tx.Table("bot.runs").Select("controller_id, controller_member_id, status, access_revoked, bot_authorization_version, binding_authorization_version").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND bot_id = ? AND conv_id = ? AND binding_id = ?", *in.RunId, in.BotId, in.ConversationId, in.BindingId).Take(&run).Error; err != nil {
				return errors.New(errors.CodeForbidden, "Run output authorization unavailable")
			}
			if run.AccessRevoked || run.Status == "stopping" || run.Status == "waiting_authorization" || run.Status == "stopped" || run.ControllerID != controller {
				return errors.New(errors.CodeForbidden, "Run output permission revoked")
			}
			var member struct{ ID string }
			if err := tx.Table("conv_members").Where("conv_id = ? AND user_id = ? AND member_type = 'user'", in.ConversationId, controller).Take(&member).Error; err != nil || run.ControllerMemberID == nil || *run.ControllerMemberID != member.ID {
				return ErrNotMember
			}
			var epoch struct {
				BotVersion     int64
				BindingVersion int64
			}
			if err := tx.Raw("SELECT b.authorization_version AS bot_version,c.authorization_version AS binding_version FROM bot.bots b JOIN messaging.conv_bots c ON c.bot_id=b.id WHERE b.id=? AND c.id=?", in.BotId, in.BindingId).Scan(&epoch).Error; err != nil {
				return err
			}
			if epoch.BotVersion != run.BotAuthorizationVersion || epoch.BindingVersion != run.BindingAuthorizationVersion {
				return errors.New(errors.CodeForbidden, "Run authorization epoch revoked")
			}
		} else if controller != "" {
			var count int64
			if err := tx.Model(&model.ConversationMember{}).Where("conv_id = ? AND user_id = ? AND member_type = 'user'", in.ConversationId, controller).Count(&count).Error; err != nil {
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
			if err := tx.Table("file.files").Select("id, name, size, ext, mime_type").Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ? AND access = 3", in.File.FileId).Take(&file).Error; err != nil {
				return errors.New(errors.CodeForbidden, "file is not publicly shareable")
			}
			msg.Content = model.FileContent{FileID: file.ID, Name: file.Name, Size: file.Size, Ext: file.Ext, MimeType: file.MimeType}.ToJSONContent()
		}
		if err := validateReplyTarget(l.ctx, l.svcCtx, tx, in.ConversationId, "", in.ReplyToId); err != nil {
			return err
		}
		// Every retry reaches all current identity, file, reply and metadata checks
		// above before observing a receipt. The conversation lock serializes keys.
		if in.OutputKey != nil {
			fingerprint, err := botOutputFingerprint(msg)
			if err != nil {
				return err
			}
			var existing botOutputSubmission
			err = tx.Where("bot_id = ? AND binding_id = ? AND output_key = ?", in.BotId, in.BindingId, *in.OutputKey).Take(&existing).Error
			if err == nil {
				same, err := sameSubmission(existing.ContentFingerprint, fingerprint)
				if err != nil {
					return err
				}
				if !same {
					return errors.New(errors.CodeConflict, "bot output key was already used with different content")
				}
				receipt = &existing
				return nil
			}
			if !stderrors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			receipt = &botOutputSubmission{BotID: in.BotId, BindingID: in.BindingId, OutputKey: *in.OutputKey, ConvID: in.ConversationId, ContentFingerprint: fingerprint}
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		msg.ID = id
		now := time.Now()
		msg.CreatedAt, msg.UpdatedAt = now, now
		if err := persistMessage(l.ctx, l.svcCtx, tx, msg, msg.SenderName); err != nil {
			return err
		}
		if receipt != nil {
			receipt.MessageID, receipt.Seq, receipt.CreatedAt = msg.ID, msg.Seq, msg.CreatedAt
			if err := tx.Create(receipt).Error; err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		l.Infof("bot message committed: message_id=%s bot_id=%s binding_id=%s", msg.ID, in.BotId, in.BindingId)
	}
	if receipt != nil {
		return &message.SendBotReplyResp{MessageId: receipt.MessageID, Seq: receipt.Seq, CreatedAt: receipt.CreatedAt.Unix()}, nil
	}
	return &message.SendBotReplyResp{MessageId: msg.ID, Seq: msg.Seq, CreatedAt: msg.CreatedAt.Unix()}, nil
}

func botOutputFingerprint(msg *model.Message) (model.JSONContent, error) {
	// Server-resolved display snapshots are not caller content: renaming a Bot
	// must not invalidate a retry of an already committed publication.
	fingerprint, err := originalSubmission(message.MessageType(msg.MsgType), msg.Content, msg.ReplyToMsgID)
	if err != nil {
		return nil, err
	}
	var metadata any
	if msg.RawPayload != "" {
		decoder := json.NewDecoder(bytes.NewBufferString(msg.RawPayload))
		decoder.UseNumber()
		if err := decoder.Decode(&metadata); err != nil {
			return nil, err
		}
	}
	fingerprint["conv_id"] = msg.ConvID
	fingerprint["bot_id"] = msg.SenderID
	fingerprint["binding_id"] = msg.BindingID
	fingerprint["requester_id"] = msg.RequesterID
	fingerprint["metadata"] = metadata
	return fingerprint, nil
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
	if enabled, exists := binding.BotSettings["enabled"]; exists && enabled != true {
		return nil, ErrBotNotInConversation
	}
	return &binding, nil
}
