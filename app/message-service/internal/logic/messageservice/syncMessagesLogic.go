package messageservicelogic

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"time"

	conversationlogic "github.com/maomeng/aim/app/message-service/internal/logic/conversationservice"
	servicemetrics "github.com/maomeng/aim/app/message-service/internal/metrics"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/sequence"
	"gorm.io/gorm"

	"github.com/zeromicro/go-zero/core/logx"
)

type SyncMessagesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSyncMessagesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SyncMessagesLogic {
	return &SyncMessagesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *SyncMessagesLogic) SyncMessages(in *message.SyncMessagesReq) (*message.SyncMessagesResp, error) {
	if in == nil || validateIdentities(in.UserId) != nil || sequence.Validate(in.Position) != nil || in.Limit < 0 {
		return nil, errors.New(errors.CodeInvalidParam, "invalid sync request")
	}
	if err := requireCaller(l.ctx, in.UserId); err != nil {
		return nil, err
	}
	limit := min(int(cmp.Or(in.Limit, 50)), cmp.Or(l.svcCtx.Config.Message.MaxPageSize, 100))
	if err := l.svcCtx.InboxRepo.EnsureStream(l.ctx, in.UserId); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "initialize inbox stream failed", err)
	}
	cutoff := time.Now().AddDate(0, 0, -cmp.Or(l.svcCtx.Config.Message.InboxRetentionDays, 30))
	var resp *message.SyncMessagesResp
	err := l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		// All projections share the same committed snapshot. A concurrent fanout
		// is either fully represented here or remains after the returned position.
		db := &database.DB{DB: tx}
		snapshot := *l.svcCtx
		snapshot.DB = db
		snapshot.InboxRepo = repo.NewInboxRepo(db)
		snapshot.MessageRepo = repo.NewMessageRepo(db).ForUser(in.UserId)
		snapshot.ConversationRepo = repo.NewConversationRepo(db)
		snapshot.ProfileRepo = repo.NewProfileRepo(db)
		page, err := snapshot.InboxRepo.ReadPage(l.ctx, in.UserId, in.Position, limit, cutoff)
		if err != nil {
			return err
		}
		resp = &message.SyncMessagesResp{NextPosition: page.NextPosition, HasMore: page.HasMore,
			RebuildRequired: page.RebuildReason != "", RebuildReason: page.RebuildReason}
		if resp.RebuildRequired {
			ids, err := snapshot.ConversationRepo.ListIDsByUser(l.ctx, in.UserId)
			if err != nil {
				return err
			}
			slices.Sort(ids)
			for _, id := range ids {
				conv, err := conversationlogic.NewGetConversationLogic(l.ctx, &snapshot).GetConversation(&message.GetConversationReq{ConversationId: id, UserId: in.UserId})
				if err != nil {
					return err
				}
				history, err := NewGetMessagesLogic(l.ctx, &snapshot).GetMessages(&message.GetMessagesReq{ConversationId: id, UserId: in.UserId,
					Pagination: &message.MessagePagination{Limit: int32(limit)}})
				if err != nil {
					return err
				}
				// History API is newest-first; rebuild emits conversation order.
				slices.Reverse(history.Messages)
				resp.Conversations = append(resp.Conversations, &message.ConversationSnapshot{Conversation: conv.Conversation, Messages: history.Messages})
			}
			return nil
		}
		msgIDs := make([]string, 0, len(page.Entries))
		for _, entry := range page.Entries {
			if entry.MessageID != nil {
				msgIDs = append(msgIDs, *entry.MessageID)
			}
		}
		msgs, err := snapshot.MessageRepo.GetByIDs(l.ctx, msgIDs)
		if err != nil {
			return err
		}
		msgMap := make(map[string]model.Message, len(msgs))
		for _, m := range msgs {
			msgMap[m.ID] = m
		}
		membership := make(map[string]bool)
		conversations := make(map[string]*message.Conversation)
		pbMsgs := make([]*message.Message, 0, len(page.Entries))
		for _, entry := range page.Entries {
			change := &message.InboxChange{Position: entry.Position, ConversationId: entry.ConvID, Kind: entry.Kind}
			if entry.Kind == model.InboxConversationRemoved {
				resp.Changes = append(resp.Changes, change)
				continue
			}
			member, checked := membership[entry.ConvID]
			if !checked {
				member, err = snapshot.ConversationRepo.IsMember(l.ctx, entry.ConvID, in.UserId)
				if err != nil {
					return err
				}
				membership[entry.ConvID] = member
			}
			if !member {
				continue
			}
			conv := conversations[entry.ConvID]
			if conv == nil {
				current, err := conversationlogic.NewGetConversationLogic(l.ctx, &snapshot).GetConversation(
					&message.GetConversationReq{ConversationId: entry.ConvID, UserId: in.UserId})
				if err != nil {
					return err
				}
				conv = current.Conversation
				conversations[entry.ConvID] = conv
			}
			change.Conversation = conv
			switch entry.Kind {
			case model.InboxConversationUpsert:
			case model.InboxReadUpdated:
				change.LastReadSeq = conv.LastReadSeq
			case model.InboxMessageNew, model.InboxMessageEdited, model.InboxMessageRecalled, model.InboxMessageDeleted:
				var m model.Message
				exists := false
				if entry.MessageID != nil {
					m, exists = msgMap[*entry.MessageID]
				}
				if entry.Kind == model.InboxMessageDeleted || !exists || m.ConvID != entry.ConvID {
					// Old new/edit references converge even after physical message deletion.
					change.Kind = model.InboxMessageDeleted
					change.MessageId = entry.MessageID
				} else {
					change.Message = modelToPbMessage(&m)
					pbMsgs = append(pbMsgs, change.Message)
				}
			default:
				return errors.New(errors.CodeInternal, "unknown persisted inbox kind")
			}
			resp.Changes = append(resp.Changes, change)
		}
		hydrateReplySummaries(l.ctx, snapshot.MessageRepo, snapshot.ProfileRepo, snapshot.ConversationRepo, pbMsgs)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "sync inbox failed", err)
	}
	if resp.RebuildRequired {
		servicemetrics.InboxSyncRebuildTotal.Inc(resp.RebuildReason)
	}
	return resp, nil
}
