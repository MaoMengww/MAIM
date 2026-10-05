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
	"github.com/maomeng/aim/pkg/pb/common"
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
	if in.UserId <= 0 || in.Limit < 0 {
		return nil, errors.New(errors.CodeInvalidParam, "invalid sync request")
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
		snapshot.MessageRepo = repo.NewMessageRepo(db)
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
					Pagination: &common.CursorPagination{Limit: int32(limit)}})
				if err != nil {
					return err
				}
				// History API is newest-first; rebuild emits conversation order.
				slices.Reverse(history.Messages)
				resp.Conversations = append(resp.Conversations, &message.ConversationSnapshot{Conversation: conv.Conversation, Messages: history.Messages})
			}
			return nil
		}
		msgIDs := make([]int64, 0, len(page.Entries))
		for _, entry := range page.Entries {
			msgIDs = append(msgIDs, entry.MessageID)
		}
		msgs, err := snapshot.MessageRepo.BatchGetByIDs(l.ctx, msgIDs)
		if err != nil {
			return err
		}
		msgMap := make(map[int64]model.Message, len(msgs))
		for _, m := range msgs {
			msgMap[m.ID] = m
		}
		membership := make(map[int64]bool)
		pbMsgs := make([]*message.Message, 0, len(page.Entries))
		for _, entry := range page.Entries {
			m, ok := msgMap[entry.MessageID]
			if !ok || m.ConvID != entry.ConvID || entry.IsDeleted {
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
			msg := modelToPbMessage(&m)
			pbMsgs = append(pbMsgs, msg)
			resp.Changes = append(resp.Changes, &message.InboxChange{Position: entry.Position,
				ConversationId: entry.ConvID, Kind: entry.Kind, Message: msg})
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
