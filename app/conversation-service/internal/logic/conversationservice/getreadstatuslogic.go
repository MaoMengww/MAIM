package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/zeromicro/go-zero/core/logx"
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
	seqs, err := l.svcCtx.Repo.GetReadSeqs(l.ctx, in.ConversationId)
	if err != nil {
		l.Logger.Errorf("get read status failed: %v", err)
		return nil, err
	}

	total, _ := l.svcCtx.Repo.CountMembers(l.ctx, in.ConversationId)
	readCount := int32(0)
	var readUsers []*conversation.ReadUser

	for _, seq := range seqs {
		if seq.LastReadSeq >= in.MessageId || in.MessageId == 0 {
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
