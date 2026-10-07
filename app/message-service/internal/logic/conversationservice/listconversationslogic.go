package conversationservice

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
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

	if err := validateRequest(l.ctx, in.UserId); err != nil {
		return nil, err
	}
	pageSize := 50
	cursor := ""
	if in.Pagination != nil {
		if in.Pagination.Limit > 0 {
			pageSize = int(in.Pagination.Limit)
		}
		if in.Pagination.Cursor != "" {
			cursor = in.Pagination.Cursor
			if identity.Validate(cursor) != nil {
				return nil, ErrInvalidParam
			}
		}
	}

	var typ *int32
	if in.Type != nil {
		v := int32(in.GetType())
		typ = &v
	}

	convs, err := l.svcCtx.ConversationRepo.ListConversationsByUser(l.ctx, in.UserId, cursor, pageSize+1, typ, in.PinnedFirst)
	if err != nil {
		l.Logger.Errorf("list conversations failed: %v", err)
		return nil, err
	}
	hasMore := len(convs) > pageSize
	if hasMore {
		convs = convs[:pageSize]
	}

	if err := l.svcCtx.MessageRepo.ProjectConversationPreviews(l.ctx, in.UserId, convs); err != nil {
		return nil, err
	}
	// 批量查询已读序列（避免 N+1）
	convIDs := make([]string, len(convs))
	for i := range convs {
		convIDs[i] = convs[i].ID
	}
	readSeqMap, err := l.svcCtx.ConversationRepo.GetReadSeqsByUser(l.ctx, convIDs, in.UserId)
	if err != nil {
		l.Logger.Errorf("get read sequences failed for user=%s: %v", in.UserId, err)
		return nil, err
	}

	// 未读由消息域本地读模型给出（唯一真相源），一次查询覆盖整页
	unreadByConv, err := l.svcCtx.ConversationRepo.UnreadCountsByUser(l.ctx, in.UserId, convIDs)
	if err != nil {
		l.Logger.Errorf("unread counts failed for user=%s: %v", in.UserId, err)
		return nil, err
	}

	var settings []model.ConvSettings
	if len(convIDs) > 0 {
		if err := l.svcCtx.DB.WithContext(l.ctx).Where("user_id = ? AND conv_id IN ?", in.UserId, convIDs).Find(&settings).Error; err != nil {
			return nil, err
		}
	}
	settingsByConv := make(map[string]model.ConvSettings, len(settings))
	for _, setting := range settings {
		settingsByConv[setting.ConvID] = setting
	}
	pbConvs := make([]*conversation.Conversation, len(convs))
	for i := range convs {
		lastReadSeq := readSeqMap[convs[i].ID]
		setting := settingsByConv[convs[i].ID]
		pbConvs[i] = toProtoConv(&convs[i], lastReadSeq, unreadByConv[convs[i].ID], setting.IsMuted, setting.IsPinned)
	}

	for _, conv := range pbConvs {
		NewCreateConversationLogic(l.ctx, l.svcCtx).resolvePrivatePeerInfo(conv, in.UserId)
	}

	nextCursor := ""
	if len(convs) > 0 {
		nextCursor = convs[len(convs)-1].ID
	}
	l.Infof("conversations listed: user_id=%s count=%d", in.UserId, len(pbConvs))
	return &conversation.ListConversationsResp{
		Conversations: pbConvs,
		Pagination: &common.CursorPaginationResp{
			NextCursor: nextCursor,
			HasMore:    hasMore,
		},
	}, nil
}
