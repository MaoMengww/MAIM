package conversationservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/metrics"
	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
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
	if err := validateRequest(l.ctx, in.CreatorId, in.MemberIds...); err != nil {
		return nil, err
	}
	if in.PeerUserId != nil && identity.Validate(*in.PeerUserId) != nil {
		return nil, ErrInvalidParam
	}
	if in.BotId != nil && identity.Validate(*in.BotId) != nil {
		return nil, ErrInvalidParam
	}
	if in.BotId != nil {
		bot, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, *in.BotId)
		if err != nil {
			return nil, ErrMemberAddFailed
		}
		if bot.Status != "active" || (bot.OwnerType != "platform" && (bot.OwnerId == nil || *bot.OwnerId != in.CreatorId)) {
			return nil, ErrInsufficientPerm
		}
	}
	convType := int32(in.Type)
	switch convType {
	case model.ConvTypePrivate:
		if (in.PeerUserId == nil) == (in.BotId == nil) || (in.PeerUserId != nil && *in.PeerUserId == in.CreatorId) || len(in.MemberIds) != 0 {
			return nil, ErrInvalidParam
		}
	case model.ConvTypeGroup:
		if in.BotId != nil || in.PeerUserId != nil {
			return nil, ErrInvalidParam
		}
	default:
		return nil, ErrInvalidConvType
	}
	findPrivate := func(r *repo.ConversationRepo) (*model.Conversation, error) {
		if in.BotId != nil {
			return r.FindPrivateBotConv(l.ctx, in.CreatorId, *in.BotId)
		}
		return r.FindPrivateConv(l.ctx, in.CreatorId, *in.PeerUserId)
	}
	if convType == model.ConvTypePrivate {
		existing, err := findPrivate(l.svcCtx.ConversationRepo)
		if err == nil {
			fetched, err := NewGetConversationLogic(l.ctx, l.svcCtx).GetConversation(&conversation.GetConversationReq{ConversationId: existing.ID, UserId: in.CreatorId})
			if err != nil {
				return nil, err
			}
			return &conversation.CreateConversationResp{ConversationId: existing.ID, Conversation: fetched.Conversation}, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	now := time.Now()
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	conv := &model.Conversation{ID: id, Type: convType, OwnerID: &in.CreatorId, CreatedAt: now, UpdatedAt: now}
	if in.Name != nil {
		conv.Name = *in.Name
	}
	if in.Avatar != nil {
		conv.Avatar = *in.Avatar
	}
	members := make([]model.ConversationMember, 0, len(in.MemberIds)+2)
	seen := make(map[string]bool, len(in.MemberIds)+2)
	addMember := func(uid string, role int32) error {
		if seen[uid] {
			return nil
		}
		memberID, err := identity.New()
		if err != nil {
			return err
		}
		members = append(members, model.ConversationMember{ID: memberID, ConvID: id, UserID: &uid, MemberType: model.MemberTypeUser, Role: role, JoinedAt: now})
		seen[uid] = true
		return nil
	}
	if err := addMember(in.CreatorId, ownerRole); err != nil {
		return nil, err
	}
	if in.PeerUserId != nil {
		if err := addMember(*in.PeerUserId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
			return nil, err
		}
	}
	for _, uid := range in.MemberIds {
		if err := addMember(uid, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
			return nil, err
		}
	}
	var convBot *model.ConvBot
	if in.BotId != nil {
		memberID, err := identity.New()
		if err != nil {
			return nil, err
		}
		relationID, err := identity.New()
		if err != nil {
			return nil, err
		}
		members = append(members, model.ConversationMember{ID: memberID, ConvID: id, MemberType: model.MemberTypeBot, BotID: in.BotId, Role: int32(conversation.MemberRole_MEMBER_ROLE_MEMBER), JoinedAt: now})
		convBot = &model.ConvBot{ID: relationID, ConvID: id, BotID: *in.BotId, AddedBy: in.CreatorId, CreatedAt: now}
	}
	conv.MemberCount = int32(len(members))
	created := false
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if convType == model.ConvTypePrivate {
			var lockKey string
			if in.BotId != nil {
				lockKey = "private-bot:" + in.CreatorId + ":" + *in.BotId
			} else {
				first, second := in.CreatorId, *in.PeerUserId
				if first > second {
					first, second = second, first
				}
				// Sorted identities identify an unordered user pair, not business order.
				lockKey = "private:" + first + ":" + second
			}
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
				return err
			}
			r := repo.NewConversationRepo(&database.DB{DB: tx})
			existing, err := findPrivate(r)
			if err == nil {
				conv = existing
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if l.svcCtx.Config.Conv.MaxMemberCount > 0 && len(members) > l.svcCtx.Config.Conv.MaxMemberCount {
			return ErrConvMaxMembers
		}
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
		if err := publishConversationChange(l.ctx, l.svcCtx, tx, conv.ID, nil, nil); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if created {
		label := "single"
		if convType == model.ConvTypeGroup {
			label = "group"
		}
		metrics.ConversationsCreatedTotal.Inc(label)
		metrics.ConvMembersTotal.Add(float64(len(members)))
		l.Infof("conversation created: conv_id=%s type=%d", conv.ID, conv.Type)
	}
	fetched, err := NewGetConversationLogic(l.ctx, l.svcCtx).GetConversation(&conversation.GetConversationReq{ConversationId: conv.ID, UserId: in.CreatorId})
	if err != nil {
		return nil, fmt.Errorf("read created conversation: %w", err)
	}
	return &conversation.CreateConversationResp{ConversationId: conv.ID, Conversation: fetched.Conversation}, nil
}

func (l *CreateConversationLogic) resolvePrivatePeerInfo(conv *conversation.Conversation, creatorID string) {
	if conv == nil || conv.Type != conversation.ConversationType_CONVERSATION_TYPE_PRIVATE {
		return
	}
	members, err := l.svcCtx.ConversationRepo.GetMembers(l.ctx, conv.Id, 0, BotResolveLimit)
	if err != nil {
		return
	}
	for _, member := range members {
		if member.UserID != nil && *member.UserID == creatorID {
			continue
		}
		if member.MemberType == model.MemberTypeBot && member.BotID != nil {
			if bot, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, *member.BotID); err == nil {
				conv.Name, conv.Avatar = bot.Name, bot.Avatar
			}
		} else if member.UserID != nil {
			if user, err := l.svcCtx.ProfileRepo.UserProfile(l.ctx, *member.UserID); err == nil {
				conv.Name, conv.Avatar = user.Username, user.Avatar
			}
		}
		return
	}
}
