package server

import (
	"context"

	conversationservicelogic "github.com/maomeng/aim/app/message-service/internal/logic/conversationservice"
	messagepb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/pb/common"
)

// Conversation RPCs. They live in the same service and process as the message
// RPCs because the conversation aggregate, its members and the read model are
// part of the message domain: checkout, member checks and the latest-message
// update share one transaction instead of a cross-service call.

func (s *MessageServiceServer) CreateConversation(ctx context.Context, in *messagepb.CreateConversationReq) (*messagepb.CreateConversationResp, error) {
	l := conversationservicelogic.NewCreateConversationLogic(ctx, s.svcCtx)
	return l.CreateConversation(in)
}

func (s *MessageServiceServer) GetConversation(ctx context.Context, in *messagepb.GetConversationReq) (*messagepb.GetConversationResp, error) {
	l := conversationservicelogic.NewGetConversationLogic(ctx, s.svcCtx)
	return l.GetConversation(in)
}

func (s *MessageServiceServer) ListConversations(ctx context.Context, in *messagepb.ListConversationsReq) (*messagepb.ListConversationsResp, error) {
	l := conversationservicelogic.NewListConversationsLogic(ctx, s.svcCtx)
	return l.ListConversations(in)
}

func (s *MessageServiceServer) UpdateConversation(ctx context.Context, in *messagepb.UpdateConversationReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewUpdateConversationLogic(ctx, s.svcCtx)
	return l.UpdateConversation(in)
}

func (s *MessageServiceServer) DeleteConversation(ctx context.Context, in *messagepb.DeleteConversationReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewDeleteConversationLogic(ctx, s.svcCtx)
	return l.DeleteConversation(in)
}

// ========== Members ==========

func (s *MessageServiceServer) AddMembers(ctx context.Context, in *messagepb.AddMembersReq) (*messagepb.AddMembersResp, error) {
	l := conversationservicelogic.NewAddMembersLogic(ctx, s.svcCtx)
	return l.AddMembers(in)
}

func (s *MessageServiceServer) RemoveMembers(ctx context.Context, in *messagepb.RemoveMembersReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewRemoveMembersLogic(ctx, s.svcCtx)
	return l.RemoveMembers(in)
}

func (s *MessageServiceServer) GetMembers(ctx context.Context, in *messagepb.GetMembersReq) (*messagepb.GetMembersResp, error) {
	l := conversationservicelogic.NewGetMembersLogic(ctx, s.svcCtx)
	return l.GetMembers(in)
}

func (s *MessageServiceServer) UpdateMember(ctx context.Context, in *messagepb.UpdateMemberReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewUpdateMemberLogic(ctx, s.svcCtx)
	return l.UpdateMember(in)
}

// ========== Group Management ==========

func (s *MessageServiceServer) MuteAll(ctx context.Context, in *messagepb.MuteAllReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewMuteAllLogic(ctx, s.svcCtx)
	return l.MuteAll(in)
}

func (s *MessageServiceServer) UnmuteAll(ctx context.Context, in *messagepb.UnmuteAllReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewUnmuteAllLogic(ctx, s.svcCtx)
	return l.UnmuteAll(in)
}

func (s *MessageServiceServer) MuteMember(ctx context.Context, in *messagepb.MuteMemberReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewMuteMemberLogic(ctx, s.svcCtx)
	return l.MuteMember(in)
}

func (s *MessageServiceServer) UnmuteMember(ctx context.Context, in *messagepb.UnmuteMemberReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewUnmuteMemberLogic(ctx, s.svcCtx)
	return l.UnmuteMember(in)
}

func (s *MessageServiceServer) SetAnnouncement(ctx context.Context, in *messagepb.SetAnnouncementReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewSetAnnouncementLogic(ctx, s.svcCtx)
	return l.SetAnnouncement(in)
}

func (s *MessageServiceServer) DeleteAnnouncement(ctx context.Context, in *messagepb.DeleteAnnouncementReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewDeleteAnnouncementLogic(ctx, s.svcCtx)
	return l.DeleteAnnouncement(in)
}

func (s *MessageServiceServer) TransferOwner(ctx context.Context, in *messagepb.TransferOwnerReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewTransferOwnerLogic(ctx, s.svcCtx)
	return l.TransferOwner(in)
}

// ========== Settings ==========

func (s *MessageServiceServer) GetSettings(ctx context.Context, in *messagepb.GetSettingsReq) (*messagepb.GetSettingsResp, error) {
	l := conversationservicelogic.NewGetSettingsLogic(ctx, s.svcCtx)
	return l.GetSettings(in)
}

func (s *MessageServiceServer) UpdateSettings(ctx context.Context, in *messagepb.UpdateSettingsReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewUpdateSettingsLogic(ctx, s.svcCtx)
	return l.UpdateSettings(in)
}

// ========== Read Status ==========

func (s *MessageServiceServer) MarkAsRead(ctx context.Context, in *messagepb.MarkAsReadReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewMarkAsReadLogic(ctx, s.svcCtx)
	return l.MarkAsRead(in)
}

func (s *MessageServiceServer) GetReadStatus(ctx context.Context, in *messagepb.GetReadStatusReq) (*messagepb.GetReadStatusResp, error) {
	l := conversationservicelogic.NewGetReadStatusLogic(ctx, s.svcCtx)
	return l.GetReadStatus(in)
}

func (s *MessageServiceServer) SendTypingEvent(ctx context.Context, in *messagepb.SendTypingEventReq) (*messagepb.SendTypingEventResp, error) {
	l := conversationservicelogic.NewSendTypingEventLogic(ctx, s.svcCtx)
	return l.SendTypingEvent(in)
}

// ========== Bot Management ==========

func (s *MessageServiceServer) AddBot(ctx context.Context, in *messagepb.AddBotReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewAddBotLogic(ctx, s.svcCtx)
	return l.AddBot(in)
}

func (s *MessageServiceServer) RemoveBot(ctx context.Context, in *messagepb.RemoveBotReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewRemoveBotLogic(ctx, s.svcCtx)
	return l.RemoveBot(in)
}

func (s *MessageServiceServer) UpdateBot(ctx context.Context, in *messagepb.UpdateBotReq) (*common.BaseResponse, error) {
	l := conversationservicelogic.NewUpdateBotLogic(ctx, s.svcCtx)
	return l.UpdateBot(in)
}

func (s *MessageServiceServer) ListBots(ctx context.Context, in *messagepb.ListBotsReq) (*messagepb.ListBotsResp, error) {
	l := conversationservicelogic.NewListBotsLogic(ctx, s.svcCtx)
	return l.ListBots(in)
}
