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

	broadcastID, err := l.svcCtx.Snowflake.Generate()
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "generate broadcast id failed", err)
	}
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

	_, err = l.resolveTargetUsers(in.Scope, scopeTargetID)
	if err != nil {
		l.Errorf("resolve target users for broadcast %d failed: %v", broadcastID, err)
		return nil, errors.Wrap(errors.CodeInternal, "resolve target users failed", err)
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
		return l.svcCtx.ProfileRepo.AllUserIDs(l.ctx)
	case "group":
		return l.svcCtx.ConversationRepo.MemberIDs(l.ctx, scopeTargetID)
	case "user":
		if scopeTargetID == 0 {
			return nil, fmt.Errorf("scope_target_id is required for user scope")
		}
		return []int64{scopeTargetID}, nil
	default:
		return nil, fmt.Errorf("unknown scope: %s", scope)
	}
}
