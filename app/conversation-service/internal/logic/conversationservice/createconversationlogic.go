package conversationservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/conversation-service/internal/metrics"
	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
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
		existing, err := l.svcCtx.Repo.FindPrivateConv(l.ctx, in.CreatorId, in.GetPeerUserId())
		if err == nil {
			conv := toProtoConv(existing, 0, 0, false, false)
			l.resolvePrivatePeerInfo(conv, in.CreatorId)
			return &conversation.CreateConversationResp{
				ConversationId: existing.ID,
				Conversation:   conv,
			}, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			l.Logger.Errorf("find private conv failed: %v", err)
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

	if err := l.svcCtx.Repo.CreateConversation(l.ctx, conv); err != nil {
		l.Logger.Errorf("create conversation failed: %v", err)
		return nil, err
	}

	ownerID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate owner id failed: %w", err)
	}
	ownerRole := int32(conversation.MemberRole_MEMBER_ROLE_OWNER)
	owner := model.ConversationMember{
		ID:       ownerID,
		ConvID:   id,
		UserID:   in.CreatorId,
		Role:     ownerRole,
		JoinedAt: now,
	}
	if err := l.svcCtx.Repo.AddMember(l.ctx, &owner); err != nil {
		l.Logger.Errorf("add owner failed: %v", err)
		return nil, err
	}

	if in.PeerUserId != nil {
		// Check if peer is a bot → create conv_bot + member with member_type="bot"
		bot, botErr := l.svcCtx.Repo.GetBot(l.ctx, in.GetPeerUserId())
		if botErr == nil {
			cbID, err := l.svcCtx.Snowflake.Generate()
			if err != nil {
				return nil, fmt.Errorf("generate conv bot id failed: %w", err)
			}
			cb := &model.ConvBot{
				ID:        cbID,
				ConvID:    id,
				BotID:     bot.ID,
				AddedBy:   in.CreatorId,
				CreatedAt: now,
			}
			botMemberID, err := l.svcCtx.Snowflake.Generate()
			if err != nil {
				return nil, fmt.Errorf("generate bot member id failed: %w", err)
			}
			botMember := &model.ConversationMember{
				ID:         botMemberID,
				ConvID:     id,
				UserID:     bot.ID,
				MemberType: model.MemberTypeBot,
				BotID:      bot.ID,
				Role:       int32(conversation.MemberRole_MEMBER_ROLE_MEMBER),
				JoinedAt:   now,
			}
			if err := l.svcCtx.Repo.AddBotWithMember(l.ctx, cb, botMember); err != nil {
				l.Logger.Errorf("add bot conv_bot failed: conv=%d, bot_id=%d, err=%v", id, bot.ID, err)
				return nil, err
			}
		} else {
			peerRole := int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)
			peerID, err := l.svcCtx.Snowflake.Generate()
			if err != nil {
				return nil, fmt.Errorf("generate peer id failed: %w", err)
			}
			peer := model.ConversationMember{
				ID:       peerID,
				ConvID:   id,
				UserID:   in.GetPeerUserId(),
				Role:     peerRole,
				JoinedAt: now,
			}
			if err := l.svcCtx.Repo.AddMember(l.ctx, &peer); err != nil {
				l.Logger.Errorf("add peer failed: %v", err)
				return nil, err
			}
		}
		if err := l.svcCtx.Repo.IncrementMemberCount(l.ctx, id, 1); err != nil {
			l.Logger.Errorf("increment member count failed: conv=%d, err=%v", id, err)
		}
	}

	if len(in.MemberIds) > 0 {
		memberRole := int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)
		var members []model.ConversationMember
		for _, uid := range in.MemberIds {
			if uid == in.CreatorId {
				continue
			}
			memberID, err := l.svcCtx.Snowflake.Generate()
			if err != nil {
				return nil, fmt.Errorf("generate member id failed: %w", err)
			}
			members = append(members, model.ConversationMember{
				ID:       memberID,
				ConvID:   id,
				UserID:   uid,
				Role:     memberRole,
				JoinedAt: now,
			})
		}
		if len(members) > 0 {
			if err := l.svcCtx.Repo.AddMembersBatch(l.ctx, members); err != nil {
				l.Logger.Errorf("add members failed: %v", err)
				return nil, err
			}
			if err := l.svcCtx.Repo.IncrementMemberCount(l.ctx, id, len(members)); err != nil {
				l.Logger.Errorf("increment member count failed: conv=%d, delta=%d, err=%v", id, len(members), err)
			}
		}
	}

	if err := l.svcCtx.Repo.IncrementMemberCount(l.ctx, id, 1); err != nil {
		l.Logger.Errorf("increment member count failed: conv=%d, err=%v", id, err)
	}

	l.Infof("conversation created: conv_id=%d type=%d", id, conv.Type)

	convTypeLabel := "single"
	if convType == int32(conversation.ConversationType_CONVERSATION_TYPE_GROUP) {
		convTypeLabel = "group"
	}
	metrics.ConversationsCreatedTotal.Inc(convTypeLabel)
	// totalMembers includes the owner and all added members
	totalMembers := 1 // owner
	if in.PeerUserId != nil {
		totalMembers++
	}
	for _, uid := range in.MemberIds {
		if uid != in.CreatorId {
			totalMembers++
		}
	}
	metrics.ConvMembersTotal.Add(float64(totalMembers))

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
	members, err := l.svcCtx.Repo.GetMembers(l.ctx, conv.Id, 0, BotResolveLimit)
	if err != nil {
		return
	}
	for _, m := range members {
		if m.UserID == creatorID {
			continue
		}
		if m.MemberType == model.MemberTypeBot {
			if bot, err := l.svcCtx.Repo.GetBot(l.ctx, m.UserID); err == nil {
				conv.Name = bot.Name
				conv.Avatar = bot.Avatar
			}
		} else if l.svcCtx.UserClient != nil {
			if user, err := l.svcCtx.UserClient.GetUserInfo(l.ctx, m.UserID); err == nil {
				conv.Name = user.Username
				conv.Avatar = user.Avatar
			}
		}
		return
	}
}
