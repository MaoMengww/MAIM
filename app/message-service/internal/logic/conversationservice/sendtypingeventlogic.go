package conversationservice

import (
	"context"
	"encoding/json"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	message "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/delivery"
)

type SendTypingEventLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewSendTypingEventLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendTypingEventLogic {
	return &SendTypingEventLogic{ctx: ctx, svcCtx: svcCtx}
}

func (l *SendTypingEventLogic) SendTypingEvent(in *message.SendTypingEventReq) (*message.SendTypingEventResp, error) {
	if err := validateRequest(l.ctx, in.UserId, in.ConversationId); err != nil {
		return nil, err
	}
	uid := in.UserId

	member, err := l.svcCtx.ConversationRepo.GetMember(l.ctx, in.ConversationId, uid)
	if err != nil || member.MemberType != model.MemberTypeUser {
		return nil, ErrNotMember
	}
	profile, err := l.svcCtx.ProfileRepo.UserProfile(l.ctx, uid)
	if err != nil {
		return nil, err
	}
	var users []string
	if err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.ConversationMember{}).
		Where("conv_id = ? AND member_type = ? AND user_id <> ?", in.ConversationId, model.MemberTypeUser, uid).Pluck("user_id", &users).Error; err != nil {
		return nil, err
	}
	eventType := "typing"
	if in.Stopped {
		eventType = "typing.stop"
	}
	raw, err := json.Marshal(map[string]any{"type": eventType, "conv_id": in.ConversationId, "user_id": uid, "username": profile.Username})
	if err != nil {
		return nil, err
	}
	if len(users) > 0 {
		if err := l.svcCtx.DeliveryPublisher.Publish(l.ctx, in.ConversationId, delivery.Intent{UserIDs: users, Payload: raw}); err != nil {
			return nil, err
		}
	}
	return &message.SendTypingEventResp{}, nil
}
