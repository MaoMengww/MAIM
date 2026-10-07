package conversationservice

import (
	"context"

	"github.com/maomeng/aim/app/message-service/internal/model"
	"github.com/maomeng/aim/app/message-service/internal/repo"
	"github.com/maomeng/aim/app/message-service/internal/svc"
	conversation "github.com/maomeng/aim/app/message-service/pb/message"
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

	if err := validateRequest(l.ctx, in.UserId, in.ConversationId); err != nil {
		return nil, err
	}
	if err := requireRole(l.ctx, l.svcCtx.ConversationRepo, in.ConversationId, in.UserId, int32(conversation.MemberRole_MEMBER_ROLE_MEMBER)); err != nil {
		return nil, err
	}
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

	members, err := l.svcCtx.ConversationRepo.GetMembers(l.ctx, in.ConversationId, offset, pageSize)
	if err != nil {
		l.Logger.Errorf("get members failed: %v", err)
		return nil, err
	}

	total, err := l.svcCtx.ConversationRepo.CountMembers(l.ctx, in.ConversationId)
	if err != nil {
		l.Logger.Errorf("count members failed: %v", err)
		return nil, err
	}
	totalPages := int32(total / int64(pageSize))
	if total%int64(pageSize) > 0 {
		totalPages++
	}

	// 收集需要获取用户信息的 user_id
	var userIDs []string
	for _, m := range members {
		if m.MemberType == model.MemberTypeUser && m.UserID != nil {
			userIDs = append(userIDs, *m.UserID)
		}
	}
	userInfoMap := make(map[string]repo.Profile)
	if len(userIDs) > 0 {
		userInfoMap, _ = l.svcCtx.ProfileRepo.UserProfiles(l.ctx, userIDs)
	}

	// 查询所有已读序列
	readSeqs, err := l.svcCtx.ConversationRepo.GetReadSeqs(l.ctx, in.ConversationId)
	if err != nil {
		l.Logger.Errorf("get member read sequences failed: %v", err)
		return nil, err
	}
	readSeqMap := make(map[string]int64, len(readSeqs))
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
		if m.MemberType == model.MemberTypeBot && m.BotID != nil {
			if bot, err := l.svcCtx.ConversationRepo.GetBot(l.ctx, *m.BotID); err == nil {
				pbMember.Username = bot.Name
				pbMember.Avatar = bot.Avatar
				pbMember.BotName = bot.Name
				pbMember.BotAvatar = bot.Avatar
			}
		} else if m.UserID != nil {
			if u, ok := userInfoMap[*m.UserID]; ok {
				pbMember.Username = u.Username
				pbMember.Avatar = u.Avatar
			}
			pbMember.LastReadSeq = readSeqMap[*m.UserID]
		}
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
