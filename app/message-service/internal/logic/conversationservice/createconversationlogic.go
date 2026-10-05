package conversationservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type CreateConversationLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewCreateConversationLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateConversationLogic {
	return &CreateConversationLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *CreateConversationLogic) CreateConversation(in *conversation.CreateConversationReq) (*conversation.CreateConversationResp, error) {
	if in.CreatorId == 0 {
		l.Logger.Error("creator_id is required")
		return nil, fmt.Errorf("creator_id is required")
	}

	convType := int32(in.GetType())

	// For private chats, return existing conversation if already exists
	if convType == int32(conversation.ConversationType_CONVERSATION_TYPE_PRIVATE) && in.PeerUserId != nil {
		existing, err := l.svcCtx.ConversationRepo.FindPrivateConv(l.ctx, in.CreatorId, in.GetPeerUserId())
		if err == nil {
			conv := toProtoConv(existing, 0, 0, false, false)
			l.resolvePrivatePeerInfo(conv, in.CreatorId)
			return &conversation.CreateConversationResp{
				ConversationId: existing.ID,
				Conversation:   conv,
			}, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	now := time.Now()
	id, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate conv id failed: %w", err)
	}

	conv := &model.Conversation{
		ID:        id,
		Type:      convType,
		OwnerID:   in.CreatorId,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if in.Name != nil {
		conv.Name = in.GetName()
	}
	if in.Avatar != nil {
		conv.Avatar = in.GetAvatar()
	}

	members := make([]model.ConversationMember, 0, len(in.MemberIds)+2)
	seen := make(map[int64]bool, len(in.MemberIds)+2)
	addMember := func(uid int64, role int32, memberType string) error {
		if seen[uid] {
			return nil
		}
		memberID, err := l.svcCtx.Snowflake.Generate()
		if err != nil {
			return fmt.Errorf("generate member id failed: %w", err)
		}
		member := model.ConversationMember{
			ID: memberID, ConvID: id, UserID: uid, MemberType: memberType,
			Role: role, JoinedAt: now,
		}
		if memberType == model.MemberTypeBot {
			member.BotID = uid
		}
		members = append(members, member)
		seen[uid] = true
		return nil
	}
	if err := addMember(in.CreatorId, ownerRole, model.MemberTypeUser); err != nil {
		return nil, err
	}
	var convBot *model.ConvBot
	if in.PeerUserId != nil && !seen[in.GetPeerUserId()] {
		memberType := model.MemberTypeUser
		bot, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, in.GetPeerUserId())
		if err == nil {
			memberType = model.MemberTypeBot
			botID, err := l.svcCtx.Snowflake.Generate()
			if err != nil {
				return nil, fmt.Errorf("generate conv bot id failed: %w", err)
			}
			convBot = &model.ConvBot{ID: botID, ConvID: id, BotID: bot.Id, AddedBy: in.CreatorId, CreatedAt: now}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err := addMember(in.GetPeerUserId(), int32(conversation.MemberRole_MEMBER_ROLE_MEMBER), memberType); err != nil {
			return nil, err
		}
	}
	for _, uid := range in.MemberIds {
		if err := addMember(uid, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER), model.MemberTypeUser); err != nil {
			return nil, err
		}
	}
	conv.MemberCount = int32(len(members))
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(conv).Error; err != nil {
			return err
		}
		if err := tx.Create(&members).Error; err != nil {
			return err
		}
		if convBot != nil {
			if err := tx.Create(convBot).Error; err != nil {
				return err
			}
		}
		return publishConversationChange(l.ctx, l.svcCtx, tx, id, nil, nil)
	})
	if err != nil {
		return nil, err
	}

	l.Infof("conversation created: conv_id=%d type=%d", id, conv.Type)

	convTypeLabel := "single"
	if convType == int32(conversation.ConversationType_CONVERSATION_TYPE_GROUP) {
		convTypeLabel = "group"
	}
	metrics.ConversationsCreatedTotal.Inc(convTypeLabel)
	metrics.ConvMembersTotal.Add(float64(len(members)))

	pbConv := toProtoConv(conv, 0, 0, false, false)
	l.resolvePrivatePeerInfo(pbConv, in.CreatorId)
	return &conversation.CreateConversationResp{
		ConversationId: id,
		Conversation:   pbConv,
	}, nil
}

func (l *CreateConversationLogic) resolvePrivatePeerInfo(conv *conversation.Conversation, creatorID int64) {
	if conv == nil || conv.Type != conversation.ConversationType_CONVERSATION_TYPE_PRIVATE {
		return
	}
	members, err := l.svcCtx.ConversationRepo.GetMembers(l.ctx, conv.Id, 0, BotResolveLimit)
	if err != nil {
		return
	}
	for _, m := range members {
		if m.UserID == creatorID {
			continue
		}
		if m.MemberType == model.MemberTypeBot {
			if bot, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, m.UserID); err == nil {
				conv.Name = bot.Name
				conv.Avatar = bot.Avatar
			}
		} else if user, err := l.svcCtx.ProfileRepo.UserProfile(l.ctx, m.UserID); err == nil {
			conv.Name = user.Username
			conv.Avatar = user.Avatar
		}
		return
	}
}
