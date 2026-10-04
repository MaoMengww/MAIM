package conversationservice

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/zeromicro/go-zero/core/logx"
	"gorm.io/gorm"
)

type GetReadStatusLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetReadStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetReadStatusLogic {
	return &GetReadStatusLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetReadStatusLogic) GetReadStatus(in *conversation.GetReadStatusReq) (*conversation.GetReadStatusResp, error) {
	seqs, err := l.svcCtx.ConversationRepo.GetReadSeqs(l.ctx, in.ConversationId)
	if err != nil {
		l.Logger.Errorf("get read status failed: %v", err)
		return nil, err
	}

	// message_id 是 Snowflake 主键，与 seq 不是同一量纲：先解析该消息在会话内
	// 的 seq，再判断谁读到了它。
	threshold := int64(0)
	if in.MessageId != 0 {
		msg, err := l.svcCtx.MessageRepo.GetByID(l.ctx, in.MessageId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrMessageNotFound
			}
			l.Logger.Errorf("get message for read status failed: %v", err)
			return nil, err
		}
		if msg.ConvID != in.ConversationId {
			return nil, ErrMessageNotFound
		}
		threshold = msg.Seq
	}

	total, err := l.svcCtx.ConversationRepo.CountMembers(l.ctx, in.ConversationId)
	if err != nil {
		l.Logger.Errorf("count members for read status failed: %v", err)
		return nil, err
	}
	readCount := int32(0)
	var readUsers []*conversation.ReadUser

	for _, seq := range seqs {
		if in.MessageId == 0 || seq.LastReadSeq >= threshold {
			readCount++
			readUsers = append(readUsers, &conversation.ReadUser{
				UserId:      seq.UserID,
				ReadAt:      seq.ReadAt.Unix(),
				LastReadSeq: seq.LastReadSeq,
			})
		}
	}

	return &conversation.GetReadStatusResp{
		ReadCount:  readCount,
		TotalCount: int32(total),
		ReadUsers:  readUsers,
	}, nil
}
