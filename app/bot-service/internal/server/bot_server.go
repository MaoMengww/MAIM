package server

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/logic/bot"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	common "github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type BotServer struct {
	svcCtx            *svc.ServiceContext
	runtime           *RuntimeServer
	conversationTools *ConversationToolServer
	pb.UnimplementedBotServiceServer
}

func NewBotServer(svcCtx *svc.ServiceContext) *BotServer {
	return &BotServer{svcCtx: svcCtx, runtime: NewRuntimeServer(svcCtx), conversationTools: NewConversationToolServer(svcCtx)}
}

func (s *BotServer) CreateBot(ctx context.Context, in *pb.CreateBotReq) (*pb.Bot, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewCreateBotLogic(ctx, s.svcCtx)
	return l.CreateBot(in)
}

func (s *BotServer) UpdateBot(ctx context.Context, in *pb.UpdateBotReq) (*pb.Bot, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewUpdateBotLogic(ctx, s.svcCtx)
	return l.UpdateBot(in)
}

func (s *BotServer) DeleteBot(ctx context.Context, in *pb.DeleteBotReq) (*common.BaseResponse, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewDeleteBotLogic(ctx, s.svcCtx)
	return l.DeleteBot(in)
}

func (s *BotServer) GetBot(ctx context.Context, in *pb.GetBotReq) (*pb.Bot, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewGetBotLogic(ctx, s.svcCtx)
	return l.GetBot(in)
}

func (s *BotServer) ListBots(ctx context.Context, in *pb.ListBotsReq) (*pb.ListBotsResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewListBotsLogic(ctx, s.svcCtx)
	return l.ListBots(in)
}

func (s *BotServer) RotateSecret(ctx context.Context, in *pb.RotateSecretReq) (*pb.RotateSecretResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewRotateSecretLogic(ctx, s.svcCtx)
	return l.RotateSecret(in)
}

func (s *BotServer) HandleIncomingWebhook(ctx context.Context, in *pb.WebhookReq) (*pb.WebhookResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewHandleIncomingWebhookLogic(ctx, s.svcCtx)
	return l.HandleIncomingWebhook(in)
}

func (s *BotServer) IssueBotToken(ctx context.Context, in *pb.IssueBotTokenReq) (*pb.IssueBotTokenResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewIssueBotTokenLogic(ctx, s.svcCtx)
	return l.IssueBotToken(in)
}

func (s *BotServer) ValidateBotToken(ctx context.Context, in *pb.ValidateBotTokenReq) (*pb.ValidateBotTokenResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewValidateBotTokenLogic(ctx, s.svcCtx)
	return l.ValidateBotToken(in)
}

func (s *BotServer) BatchGetBots(ctx context.Context, in *pb.BatchGetBotsReq) (*pb.BatchGetBotsResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewBatchGetBotsLogic(ctx, s.svcCtx)
	return l.BatchGetBots(in)
}

func (s *BotServer) GetBotWebhookConfig(ctx context.Context, in *pb.GetBotWebhookConfigReq) (*pb.GetBotWebhookConfigResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewGetBotWebhookConfigLogic(ctx, s.svcCtx)
	return l.GetBotWebhookConfig(in)
}

// ========== MCP Server Management ==========

func (s *BotServer) CreateMcpServer(ctx context.Context, in *pb.CreateMcpServerReq) (*pb.McpServerInfo, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewCreateMcpServerLogic(ctx, s.svcCtx)
	return l.CreateMcpServer(in)
}

func (s *BotServer) UpdateMcpServer(ctx context.Context, in *pb.UpdateMcpServerReq) (*pb.McpServerInfo, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewUpdateMcpServerLogic(ctx, s.svcCtx)
	return l.UpdateMcpServer(in)
}

func (s *BotServer) DeleteMcpServer(ctx context.Context, in *pb.DeleteMcpServerReq) (*common.BaseResponse, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewDeleteMcpServerLogic(ctx, s.svcCtx)
	return l.DeleteMcpServer(in)
}

func (s *BotServer) GetMcpServer(ctx context.Context, in *pb.GetMcpServerReq) (*pb.McpServerInfo, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewGetMcpServerLogic(ctx, s.svcCtx)
	return l.GetMcpServer(in)
}

func (s *BotServer) ListMcpServers(ctx context.Context, in *pb.ListMcpServersReq) (*pb.ListMcpServersResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewListMcpServersLogic(ctx, s.svcCtx)
	return l.ListMcpServers(in)
}

// ========== Bot MCP Assignment ==========

func (s *BotServer) AssignMcpToBot(ctx context.Context, in *pb.AssignMcpToBotReq) (*common.BaseResponse, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewAssignMcpToBotLogic(ctx, s.svcCtx)
	return l.AssignMcpToBot(in)
}

func (s *BotServer) UnassignMcpFromBot(ctx context.Context, in *pb.UnassignMcpFromBotReq) (*common.BaseResponse, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewUnassignMcpFromBotLogic(ctx, s.svcCtx)
	return l.UnassignMcpFromBot(in)
}

func (s *BotServer) ListBotMcpServers(ctx context.Context, in *pb.ListBotMcpServersReq) (*pb.ListBotMcpServersResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewListBotMcpServersLogic(ctx, s.svcCtx)
	return l.ListBotMcpServers(in)
}

func (s *BotServer) UpdateBotMcpServer(ctx context.Context, in *pb.UpdateBotMcpServerReq) (*common.BaseResponse, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewUpdateBotMcpServerLogic(ctx, s.svcCtx)
	return l.UpdateBotMcpServer(in)
}

// ========== MCP Tool Discovery ==========

func (s *BotServer) DiscoverMcpTools(ctx context.Context, in *pb.DiscoverMcpToolsReq) (*pb.DiscoverMcpToolsResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewDiscoverMcpToolsLogic(ctx, s.svcCtx)
	return l.DiscoverMcpTools(in)
}

func (s *BotServer) ListMcpTools(ctx context.Context, in *pb.ListMcpToolsReq) (*pb.ListMcpToolsResp, error) {
	if s.svcCtx.Config.Role == "runtime" {
		return nil, status.Error(codes.Unimplemented, "control API is not served by a runtime replica")
	}
	l := bot.NewListMcpToolsLogic(ctx, s.svcCtx)
	return l.ListMcpTools(in)
}

// Preserve caller identity, locale and request metadata across the internal runtime hop.
func forwardRuntimeContext(ctx context.Context) context.Context {
	incoming, _ := metadata.FromIncomingContext(ctx)
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	return metadata.NewOutgoingContext(ctx, metadata.Join(outgoing, incoming))
}

func (s *BotServer) StreamChat(req *pb.StreamChatReq, stream grpc.ServerStreamingServer[pb.StreamChatResp]) error {
	return s.runtime.StreamChat(req, stream)
}

func (s *BotServer) SummarizeConversation(ctx context.Context, req *pb.SummarizeReq) (*pb.SummarizeResp, error) {
	return s.conversationTools.SummarizeConversation(ctx, req)
}

func (s *BotServer) GetConvSummaries(ctx context.Context, req *pb.GetConvSummariesReq) (*pb.GetConvSummariesResp, error) {
	return s.conversationTools.GetConvSummaries(ctx, req)
}

func (s *BotServer) CreateTodo(ctx context.Context, req *pb.CreateTodoReq) (*pb.TodoItem, error) {
	return s.conversationTools.CreateTodo(ctx, req)
}

func (s *BotServer) UpdateTodo(ctx context.Context, req *pb.UpdateTodoReq) (*emptypb.Empty, error) {
	return s.conversationTools.UpdateTodo(ctx, req)
}

func (s *BotServer) DeleteTodo(ctx context.Context, req *pb.DeleteTodoReq) (*emptypb.Empty, error) {
	return s.conversationTools.DeleteTodo(ctx, req)
}

func (s *BotServer) GenerateReplyCandidates(ctx context.Context, req *pb.ReplyCandidatesReq) (*pb.ReplyCandidatesResp, error) {
	return s.conversationTools.GenerateReplyCandidates(ctx, req)
}

func (s *BotServer) TranslateMessage(ctx context.Context, req *pb.TranslateMessageReq) (*pb.TranslateMessageResp, error) {
	return s.conversationTools.TranslateMessage(ctx, req)
}
