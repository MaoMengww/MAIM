package messageservicelogic

import (
	"context"
	stderrors "errors"
	"slices"
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

type SendBroadcastLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSendBroadcastLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SendBroadcastLogic {
	return &SendBroadcastLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SendBroadcastLogic) SendBroadcast(in *message.SendBroadcastReq) (*message.SendBroadcastResp, error) {
	if in == nil || validateIdentities(in.SenderId) != nil || (in.ScopeTargetId != nil && validateIdentities(*in.ScopeTargetId) != nil) {
		return nil, errors.New(errors.CodeInvalidParam, "invalid broadcast identity")
	}
	if in.Content == "" {
		return nil, ErrBroadcastContentRequired
	}

	scopeTargetID := in.ScopeTargetId
	users, err := l.resolveTargetUsers(in.Scope, scopeTargetID)
	if err != nil {
		if _, ok := errors.IsBizError(err); ok {
			return nil, err
		}
		return nil, errors.Wrap(errors.CodeInternal, "resolve target users failed", err)
	}
	// Every broadcast locks recipients in the same order, including when scopes
	// overlap, so multi-user broadcasts cannot deadlock on their system chats.
	slices.Sort(users)
	users = slices.Compact(users)
	broadcastID, err := identity.New()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate broadcast id failed", err)
	}
	now := time.Now()
	content := broadcastContentJSON(in.Content)
	detail, _ := content["text"].(string)
	if detail == "" {
		detail = in.Content
	}
	systemContent := model.SystemContent{
		Action: "broadcast", Detail: detail, ActorID: &in.SenderId,
		ActorType: "system", Payload: in.Content,
	}.ToJSONContent()
	err = l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.Broadcast{
			ID: broadcastID, SenderID: &in.SenderId, Content: content,
			Scope: in.Scope, ScopeTargetID: scopeTargetID, CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		for _, userID := range users {
			conv, err := l.systemConversation(tx, userID)
			if err != nil {
				return err
			}
			msgID, err := identity.New()
			if err != nil {
				return err
			}
			msg := &model.Message{
				ID: msgID, ConvID: conv.ID, SenderID: &in.SenderId, SenderType: "system",
				MsgType: model.MsgTypeSystem, Content: systemContent,
				Status: model.MessageStatusNormal, EditHistory: model.JSONArray{},
				CreatedAt: now, UpdatedAt: now,
			}
			if err := persistMessage(l.ctx, l.svcCtx, tx, msg, "system"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "insert broadcast messages failed", err)
	}

	return &message.SendBroadcastResp{
		BroadcastId: broadcastID,
		CreatedAt:   now.Unix(),
	}, nil
}

func (l *SendBroadcastLogic) resolveTargetUsers(scope string, scopeTargetID *string) ([]string, error) {
	switch scope {
	case "all":
		return l.svcCtx.ProfileRepo.AllUserIDs(l.ctx)
	case "group":
		if scopeTargetID == nil {
			return nil, errors.New(errors.CodeInvalidParam, "scope_target_id is required for group scope")
		}
		var users []string
		err := l.svcCtx.DB.WithContext(l.ctx).Model(&model.ConversationMember{}).
			Where("conv_id = ? AND member_type = ?", *scopeTargetID, model.MemberTypeUser).
			Pluck("user_id", &users).Error
		return users, err
	case "user":
		if scopeTargetID == nil {
			return nil, errors.New(errors.CodeInvalidParam, "scope_target_id is required for user scope")
		}
		if _, err := l.svcCtx.ProfileRepo.UserProfile(l.ctx, *scopeTargetID); err != nil {
			if stderrors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errors.New(errors.CodeNotFound, "broadcast recipient not found")
			}
			return nil, err
		}
		return []string{*scopeTargetID}, nil
	default:
		return nil, errors.New(errors.CodeInvalidParam, "unknown broadcast scope")
	}
}

// The partial unique index arbitrates concurrent first broadcasts. A conflicting
// insert waits for its winner; the following locked read observes that commit.
func (l *SendBroadcastLogic) systemConversation(tx *gorm.DB, userID string) (*model.Conversation, error) {
	var conv model.Conversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("type = ? AND owner_id = ?", model.ConvTypeSystem, userID).Take(&conv).Error
	if err == nil {
		return &conv, nil
	}
	if !stderrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	convID, err := identity.New()
	if err != nil {
		return nil, err
	}
	memberID, err := identity.New()
	if err != nil {
		return nil, err
	}
	conv = model.Conversation{
		ID: convID, Type: model.ConvTypeSystem, OwnerID: &userID,
		Name: "系统通知", MemberCount: 1,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:     []clause.Column{{Name: "owner_id"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "type = 3"}}},
		DoNothing:   true,
	}).Create(&conv).Error; err != nil {
		return nil, err
	}
	conv = model.Conversation{}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("type = ? AND owner_id = ?", model.ConvTypeSystem, userID).Take(&conv).Error; err != nil {
		return nil, err
	}
	member := model.ConversationMember{
		ID: memberID, ConvID: conv.ID, UserID: &userID, MemberType: model.MemberTypeUser,
		Role: int32(message.MemberRole_MEMBER_ROLE_MEMBER),
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "conv_id"}, {Name: "user_id"}}, DoNothing: true,
	}).Create(&member).Error; err != nil {
		return nil, err
	}
	return &conv, nil
}
