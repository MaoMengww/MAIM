package messageservicelogic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	"github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/event"

	"github.com/zeromicro/go-zero/core/logx"
)

const inboxBatchSize = 500

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
	if in.Content == "" {
		return nil, ErrBroadcastContentRequired
	}

	broadcastID := l.svcCtx.Snowflake.Generate()
	now := time.Now()

	var scopeTargetID int64
	if in.ScopeTargetId != nil {
		scopeTargetID = *in.ScopeTargetId
	}

	broadcast := &model.Broadcast{
		ID:            broadcastID,
		SenderID:      in.SenderId,
		Content:       broadcastContentJSON(in.Content),
		Scope:         in.Scope,
		ScopeTargetID: scopeTargetID,
		CreatedAt:     now,
	}

	broadcastRepo := l.svcCtx.BroadcastRepo
	if err := broadcastRepo.Insert(l.ctx, broadcast); err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "insert broadcast failed", err)
	}

	targetUsers, err := l.resolveTargetUsers(in.Scope, scopeTargetID)
	if err != nil {
		l.Errorf("resolve target users for broadcast %d failed: %v", broadcastID, err)
		return nil, errors.Wrap(errors.CodeInternal, "resolve target users failed", err)
	}

	if len(targetUsers) > 0 {
		if err := l.writeInboxEntries(targetUsers, broadcastID, now); err != nil {
			l.Errorf("write inbox entries for broadcast %d failed: %v", broadcastID, err)
			return nil, errors.Wrap(errors.CodeInternal, "write inbox entries failed", err)
		}
	}

	kafkaMsg := event.BroadcastCreatedEvent{
		BroadcastID:   broadcastID,
		SenderID:      in.SenderId,
		Content:       in.Content,
		Scope:         in.Scope,
		ScopeTargetID: scopeTargetID,
		CreatedAt:     now.Unix(),
	}
	kafkaVal, marshalErr := json.Marshal(kafkaMsg)
	if marshalErr != nil {
		l.Errorf("json marshal failed for message.created (broadcast): broadcast_id=%d, err=%v", broadcastID, marshalErr)
	} else if err := l.svcCtx.MessageCreatedProducer.Send(l.ctx, fmt.Sprintf("broadcast:%d", broadcastID), kafkaVal); err != nil {
		l.Errorf("kafka produce message.created (broadcast) failed: broadcast_id=%d, err=%v", broadcastID, err)
	}

	return &message.SendBroadcastResp{
		BroadcastId: broadcastID,
		CreatedAt:   now.Unix(),
	}, nil
}

func (l *SendBroadcastLogic) resolveTargetUsers(scope string, scopeTargetID int64) ([]int64, error) {
	switch scope {
	case "all":
		return l.svcCtx.UserClient.ListAllIDs(l.ctx)
	case "group":
		return l.svcCtx.ConvClient.GetMembers(l.ctx, scopeTargetID)
	case "user":
		if scopeTargetID == 0 {
			return nil, fmt.Errorf("scope_target_id is required for user scope")
		}
		return []int64{scopeTargetID}, nil
	default:
		return nil, fmt.Errorf("unknown scope: %s", scope)
	}
}

func (l *SendBroadcastLogic) writeInboxEntries(userIDs []int64, broadcastID int64, now time.Time) error {
	for i := 0; i < len(userIDs); i += inboxBatchSize {
		end := i + inboxBatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		batch := userIDs[i:end]
		inboxes := make([]model.UserInbox, 0, len(batch))
		for _, uid := range batch {
			inboxes = append(inboxes, model.UserInbox{
				UserID:    uid,
				ConvID:    0, // 0 means broadcast
				MessageID: broadcastID,
				Seq:       broadcastID,
				CreatedAt: now,
			})
		}
		if err := l.svcCtx.InboxRepo.BatchInsert(l.ctx, inboxes); err != nil {
			return err
		}
	}
	return nil
}
