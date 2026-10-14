package messageservicelogic

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/sequence"
	"gorm.io/gorm"
)

type botHistoryGrant struct {
	ID        string
	BindingID string
	GrantedBy string
	StartSeq  int64
	EndSeq    int64
	CreatedAt time.Time
}

func (botHistoryGrant) TableName() string { return "bot_history_grants" }

type botHistoryRead struct {
	ID        string
	GrantID   string
	BindingID string
	StartSeq  int64
	EndSeq    int64
	CreatedAt time.Time
}

func (botHistoryRead) TableName() string { return "bot_history_reads" }

// GrantBotHistory makes the human's explicit range durable; ownership alone is
// deliberately insufficient. Visibility is rechecked again at each Bot read.
func GrantBotHistory(ctx context.Context, s *svc.ServiceContext, in *message.GrantBotHistoryReq) (*message.GrantBotHistoryResp, error) {
	if in == nil || validateIdentities(in.ConversationId, in.BotId, in.BindingId, in.UserId) != nil || in.StartSeq < 1 || in.EndSeq < in.StartSeq || sequence.Validate(in.EndSeq) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid history grant")
	}
	if err := requireCaller(ctx, in.UserId); err != nil {
		return nil, err
	}
	id, err := identity.New()
	if err != nil {
		return nil, err
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockBotBinding(ctx, tx, in.ConversationId, in.BotId, in.BindingId); err != nil {
			return err
		}
		var conv model.Conversation
		if err := tx.Where("id = ?", in.ConversationId).Take(&conv).Error; err != nil {
			return err
		}
		if in.EndSeq > conv.MaxSeq {
			return errors.New(errors.CodeInvalidParam, "history grant exceeds committed history")
		}
		var member model.ConversationMember
		if err := tx.Where("conv_id = ? AND user_id = ?", in.ConversationId, in.UserId).Take(&member).Error; err != nil {
			return ErrNotMember
		}
		var hidden int64
		if err := tx.Model(&model.Message{}).Where("conv_id = ? AND seq BETWEEN ? AND ?", in.ConversationId, in.StartSeq, in.EndSeq).
			Where("status = ? OR EXISTS (SELECT 1 FROM personal_message_deletions d WHERE d.user_id = ? AND d.conv_id = messages.conv_id AND d.message_id = messages.id)", model.MessageStatusRecalled, in.UserId).Count(&hidden).Error; err != nil {
			return err
		}
		if hidden > 0 {
			return errors.New(errors.CodeForbidden, "history range contains unavailable sources")
		}
		return tx.Create(&botHistoryGrant{ID: id, BindingID: in.BindingId, GrantedBy: in.UserId, StartSeq: in.StartSeq, EndSeq: in.EndSeq, CreatedAt: time.Now()}).Error
	})
	if err != nil {
		return nil, err
	}
	return &message.GrantBotHistoryResp{GrantId: id}, nil
}

func (l *GetMessagesLogic) getBotHistory(in *message.GetMessagesReq) (*message.GetMessagesResp, error) {
	if validateIdentities(in.ConversationId, in.GetBotId(), in.GetBindingId(), in.GetHistoryGrantId()) != nil || in.UserId != "" || callerUserID(l.ctx) != "" {
		return nil, errors.New(errors.CodeForbidden, "bot history requires its own binding and explicit grant")
	}
	limit := 30
	var cursor int64
	if in.Pagination != nil {
		if in.Pagination.Limit < 0 || sequence.Validate(in.Pagination.Cursor) != nil {
			return nil, ErrInvalidSeq
		}
		if in.Pagination.Limit > 0 {
			limit = min(int(in.Pagination.Limit), 100)
		}
		cursor = in.Pagination.Cursor
	}
	var result *message.GetMessagesResp
	err := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockBotBinding(l.ctx, tx, in.ConversationId, in.GetBotId(), in.GetBindingId()); err != nil {
			return err
		}
		var grant botHistoryGrant
		if err := tx.Where("id = ? AND binding_id = ?", in.GetHistoryGrantId(), in.GetBindingId()).Take(&grant).Error; err != nil {
			return errors.New(errors.CodeForbidden, "history grant unavailable")
		}
		var count int64
		if err := tx.Model(&model.ConversationMember{}).Where("conv_id = ? AND user_id = ?", in.ConversationId, grant.GrantedBy).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New(errors.CodeForbidden, "history grant author is no longer a participant")
		}
		if cursor != 0 && (cursor < grant.StartSeq || cursor > grant.EndSeq) {
			return errors.New(errors.CodeForbidden, "history cursor is outside the grant")
		}
		q := tx.Model(&model.Message{}).Where("conv_id = ? AND seq BETWEEN ? AND ?", in.ConversationId, grant.StartSeq, grant.EndSeq).
			Where("status <> ?", model.MessageStatusRecalled).
			Where("NOT EXISTS (SELECT 1 FROM personal_message_deletions d WHERE d.user_id = ? AND d.conv_id = messages.conv_id AND d.message_id = messages.id)", grant.GrantedBy)
		if cursor > 0 {
			q = q.Where("seq < ?", cursor)
		}
		var msgs []model.Message
		if err := q.Order("seq DESC").Limit(limit + 1).Find(&msgs).Error; err != nil {
			return err
		}
		more := len(msgs) > limit
		if more {
			msgs = msgs[:limit]
		}
		if err := repo.NewMessageRepo(&database.DB{DB: tx}).ForUser(grant.GrantedBy).ProjectKnowledgeResults(l.ctx, msgs); err != nil {
			return err
		}
		result = &message.GetMessagesResp{Pagination: &message.MessagePaginationResp{HasMore: more}}
		for i := range msgs {
			result.Messages = append(result.Messages, modelToPbMessage(&msgs[i]))
		}
		// No unbounded reply hydration: a reference never expands the authorized range.
		if more {
			result.Pagination.NextCursor = msgs[len(msgs)-1].Seq
		}
		id, err := identity.New()
		if err != nil {
			return err
		}
		start, end := grant.StartSeq, grant.EndSeq
		if cursor > 0 {
			end = max(start, cursor-1)
		}
		if len(msgs) > 0 {
			start = msgs[len(msgs)-1].Seq
			end = msgs[0].Seq
		}
		return tx.Create(&botHistoryRead{ID: id, GrantID: grant.ID, BindingID: in.GetBindingId(), StartSeq: start, EndSeq: end, CreatedAt: time.Now()}).Error
	})
	return result, err
}
