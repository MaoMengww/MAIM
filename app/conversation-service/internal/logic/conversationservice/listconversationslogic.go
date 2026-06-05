package conversationservice

import (
	"context"
	"strconv"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type ListConversationsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListConversationsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListConversationsLogic {
	return &ListConversationsLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *ListConversationsLogic) ListConversations(in *conversation.ListConversationsReq) (*conversation.ListConversationsResp, error) {
	pageSize := 50
	cursor := int64(0)
	if in.Pagination != nil {
		if in.Pagination.Limit > 0 {
			pageSize = int(in.Pagination.Limit)
		}
		if in.Pagination.Cursor != "" {
			cursor, _ = strconv.ParseInt(in.Pagination.Cursor, 10, 64)
		}
	}

	var typ *int32
	if in.Type != nil {
		v := int32(in.GetType())
		typ = &v
	}

	var convs []model.Conversation

	if in.PinnedFirst {
		pinnedConvs, _ := l.svcCtx.Repo.ListConversationsByUserPinned(l.ctx, in.UserId, true)
		convs = append(convs, pinnedConvs...)
	}

	remain := pageSize - len(convs)
	if remain > 0 {
		rest, err := l.svcCtx.Repo.ListConversationsByUser(l.ctx, in.UserId, cursor, remain, typ)
		if err != nil {
			l.Logger.Errorf("list conversations failed: %v", err)
			return nil, err
		}
		convs = append(convs, rest...)
	}

	// 批量查询已读序列（避免 N+1）
	convIDs := make([]int64, len(convs))
	for i := range convs {
		convIDs[i] = convs[i].ID
	}
	readSeqMap, _ := l.svcCtx.Repo.GetReadSeqsByUser(l.ctx, convIDs, in.UserId)

	pbConvs := make([]*conversation.Conversation, len(convs))
	for i := range convs {
		// 从Redis获取未读数
		unread := int32(0)
		if l.svcCtx.UnreadCache != nil {
			c, err := l.svcCtx.UnreadCache.GetUnreadCount(l.ctx, in.UserId, convs[i].ID)
			if err == nil {
				unread = c
			}
		}
		lastReadSeq := readSeqMap[convs[i].ID]
		pbConvs[i] = toProtoConv(&convs[i], lastReadSeq, unread, false, false)
	}

	// Resolve peer info for private conversations
	for i, conv := range convs {
		if convs[i].Type == 1 { // PRIVATE
			members, err := l.svcCtx.Repo.GetMembers(l.ctx, convs[i].ID, 0, BotResolveLimit)
			if err == nil {
				for _, m := range members {
					if m.UserID != in.UserId {
						if m.MemberType == model.MemberTypeBot {
							if bot, err := l.svcCtx.Repo.GetBot(l.ctx, m.UserID); err == nil {
								pbConvs[i].Name = bot.Name
								pbConvs[i].Avatar = bot.Avatar
							}
						} else if l.svcCtx.UserClient != nil {
							if user, err := l.svcCtx.UserClient.GetUserInfo(l.ctx, m.UserID); err == nil {
								pbConvs[i].Name = user.Username
								pbConvs[i].Avatar = user.Avatar
							}
						}
						break
					}
				}
			}
		}
		_ = conv
	}

	l.Infof("conversations listed: user_id=%d count=%d", in.UserId, len(pbConvs))
	return &conversation.ListConversationsResp{
		Conversations: pbConvs,
		Pagination: &common.CursorPaginationResp{
			HasMore: int32(len(convs)) >= int32(pageSize),
		},
	}, nil
}
