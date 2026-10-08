package messageservicelogic

import (
	"context"
	stderrors "errors"
	"slices"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetMessageByIDLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetMessageByIDLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetMessageByIDLogic {
	return &GetMessageByIDLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetMessageByIDLogic) GetMessageByID(in *message.GetMessageByIDReq) (*message.GetMessageByIDResp, error) {
	if in == nil || validateIdentities(in.MessageId) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid message identity")
	}
	if in.BotId != nil || in.BindingId != nil || in.RequesterId != nil || in.ExplicitReference {
		return l.getBotSource(in)
	}
	callerID := callerUserID(l.ctx)
	if callerID == "" {
		return nil, ErrUserIDMissing
	}
	// Authorization precedes account visibility: a non-member is forbidden, while a
	// member whose own deletion overlay hides the message must see it as absent.
	msg, err := l.svcCtx.MessageRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	// 读取前校验：调用者必须是该消息所属会话的成员
	isMember, err := l.svcCtx.ConversationRepo.IsMember(l.ctx, msg.ConvID, callerID)
	if err != nil {
		return nil, ErrMemberCheckFailed
	}
	if !isMember {
		return nil, ErrNotMember
	}

	msgRepo := l.svcCtx.MessageRepo.ForUser(callerID)
	msg, err = msgRepo.GetByID(l.ctx, in.MessageId)
	if err != nil {
		return nil, errors.Wrap(errors.CodeNotFound, "message not found", err)
	}

	pbMsg := modelToPbMessage(msg)
	hydrateReplySummaries(l.ctx, msgRepo, l.svcCtx.ProfileRepo, l.svcCtx.ConversationRepo, []*message.Message{pbMsg})
	return &message.GetMessageByIDResp{
		Message: pbMsg,
	}, nil
}

// An explicit selection grants this source only. Subscription inputs must also
// prove the recorded human mention belongs to the captured binding lifecycle.
func (l *GetMessageByIDLogic) getBotSource(in *message.GetMessageByIDReq) (*message.GetMessageByIDResp, error) {
	if callerUserID(l.ctx) != "" || validateIdentities(in.GetBotId(), in.GetBindingId(), in.GetRequesterId()) != nil {
		return nil, errors.New(errors.CodeForbidden, "bot source requires its own binding and requester")
	}
	var source model.Message
	err := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		var captured model.ConvBot
		if err := tx.Where("id = ? AND bot_id = ?", in.GetBindingId(), in.GetBotId()).Take(&captured).Error; err != nil {
			return ErrBotNotInConversation
		}
		binding, err := lockBotBinding(l.ctx, tx, captured.ConvID, in.GetBotId(), in.GetBindingId())
		if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.ConversationMember{}).Where("conv_id = ? AND user_id = ?", binding.ConvID, in.GetRequesterId()).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrNotMember
		}
		if err := tx.Where("id = ? AND conv_id = ? AND status <> ?", in.MessageId, binding.ConvID, model.MessageStatusRecalled).
			Where("NOT EXISTS (SELECT 1 FROM personal_message_deletions d WHERE d.user_id = ? AND d.conv_id = messages.conv_id AND d.message_id = messages.id)", in.GetRequesterId()).Take(&source).Error; err != nil {
			if stderrors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New(errors.CodeForbidden, "selected bot source is unavailable")
			}
			return err
		}
		if !in.ExplicitReference {
			if source.SenderType != "user" || source.SenderID == nil || *source.SenderID != in.GetRequesterId() || source.Seq <= binding.StartSeq || source.MsgType != model.MsgTypeText {
				return errors.New(errors.CodeForbidden, "message is not a current human bot request")
			}
			text := model.ParseTextContent(source.Content)
			if !slices.Contains(text.MentionUserIDs, in.GetBotId()) {
				return errors.New(errors.CodeForbidden, "message does not mention this bot")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// No reply hydration: a selected message cannot expand its own grant.
	return &message.GetMessageByIDResp{Message: modelToPbMessage(&source)}, nil
}
