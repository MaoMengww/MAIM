package messageservicelogic

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"

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
	callerID := callerUserID(l.ctx)
	if callerID == 0 {
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
