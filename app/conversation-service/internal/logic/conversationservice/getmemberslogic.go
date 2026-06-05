package conversationservice

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"

	"github.com/maomeng/aim/app/conversation-service/internal/model"
	"github.com/maomeng/aim/app/conversation-service/internal/svc"
	"github.com/maomeng/aim/app/conversation-service/pb/conversation"
	"github.com/maomeng/aim/pkg/pb/common"
	"github.com/zeromicro/go-zero/core/logx"
)

type GetMembersLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetMembersLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetMembersLogic {
	return &GetMembersLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}
}

func (l *GetMembersLogic) GetMembers(in *conversation.GetMembersReq) (*conversation.GetMembersResp, error) {
	page := 1
	pageSize := 50
	if in.Pagination != nil {
		if in.Pagination.Page > 0 {
			page = int(in.Pagination.Page)
		}
		if in.Pagination.PageSize > 0 {
			pageSize = int(in.Pagination.PageSize)
		}
	}
	offset := (page - 1) * pageSize

	members, err := l.svcCtx.Repo.GetMembers(l.ctx, in.ConversationId, offset, pageSize)
	if err != nil {
		l.Logger.Errorf("get members failed: %v", err)
		return nil, err
	}

	total, err := l.svcCtx.Repo.CountMembers(l.ctx, in.ConversationId)
	if err != nil {
		l.Logger.Errorf("count members failed: %v", err)
		return nil, err
	}
	totalPages := int32(total / int64(pageSize))
	if total%int64(pageSize) > 0 {
		totalPages++
	}

	// 收集需要获取用户信息的 user_id
	var userIDs []int64
	for _, m := range members {
		if m.MemberType != model.MemberTypeBot {
			userIDs = append(userIDs, m.UserID)
		}
	}
	userInfoMap := make(map[int64]*userpb.UserInfo)
	if len(userIDs) > 0 && l.svcCtx.UserClient != nil {
		userInfoMap, _ = l.svcCtx.UserClient.BatchGetUserInfo(l.ctx, userIDs)
	}

	// 查询所有已读序列
	readSeqs, _ := l.svcCtx.Repo.GetReadSeqs(l.ctx, in.ConversationId)
	readSeqMap := make(map[int64]int64, len(readSeqs))
	for _, rs := range readSeqs {
		readSeqMap[rs.UserID] = rs.LastReadSeq
	}

	pbMembers := make([]*conversation.ConversationMember, len(members))
	for i, m := range members {
		memberType := conversation.MemberType_MEMBER_TYPE_USER
		if m.MemberType == model.MemberTypeBot {
			memberType = conversation.MemberType_MEMBER_TYPE_BOT
		}
		pbMember := &conversation.ConversationMember{
			UserId:     m.UserID,
			Role:       conversation.MemberRole(m.Role),
			Alias:      m.Alias,
			JoinedAt:   m.JoinedAt.Unix(),
			IsMuted:    m.IsMuted,
			MuteUntil:  m.MuteUntil,
			MemberType: memberType,
			BotId:      m.BotID,
		}
		if m.MemberType == model.MemberTypeBot {
			if bot, err := l.svcCtx.Repo.GetBot(l.ctx, m.BotID); err == nil {
				pbMember.Username = bot.Name
				pbMember.Avatar = bot.Avatar
				pbMember.BotName = bot.Name
				pbMember.BotAvatar = bot.Avatar
			}
		} else if u, ok := userInfoMap[m.UserID]; ok {
			pbMember.Username = u.GetUsername()
			pbMember.Avatar = u.GetAvatar()
		}
		pbMember.LastReadSeq = readSeqMap[m.UserID]
		pbMembers[i] = pbMember
	}

	return &conversation.GetMembersResp{
		Members: pbMembers,
		Pagination: &common.PaginationResp{
			Page:       int32(page),
			PageSize:   int32(pageSize),
			Total:      total,
			TotalPages: totalPages,
		},
	}, nil
}
