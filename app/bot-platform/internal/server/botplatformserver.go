package server

import (
	"context"

	"github.com/maomeng/aim/app/bot-platform/internal/logic/botplatform"
	"github.com/maomeng/aim/app/bot-platform/internal/svc"
	pb "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	common "github.com/maomeng/aim/pkg/pb/common"
)

type BotPlatformServer struct {
	svcCtx *svc.ServiceContext
	pb.UnimplementedBotPlatformServer
}

func NewBotPlatformServer(svcCtx *svc.ServiceContext) *BotPlatformServer {
	return &BotPlatformServer{svcCtx: svcCtx}
}

func (s *BotPlatformServer) CreateBot(ctx context.Context, in *pb.CreateBotReq) (*pb.Bot, error) {
	l := botplatform.NewCreateBotLogic(ctx, s.svcCtx)
	return l.CreateBot(in)
}

func (s *BotPlatformServer) UpdateBot(ctx context.Context, in *pb.UpdateBotReq) (*pb.Bot, error) {
	l := botplatform.NewUpdateBotLogic(ctx, s.svcCtx)
	return l.UpdateBot(in)
}

func (s *BotPlatformServer) DeleteBot(ctx context.Context, in *pb.DeleteBotReq) (*common.BaseResponse, error) {
	l := botplatform.NewDeleteBotLogic(ctx, s.svcCtx)
	return l.DeleteBot(in)
}

func (s *BotPlatformServer) GetBot(ctx context.Context, in *pb.GetBotReq) (*pb.Bot, error) {
	l := botplatform.NewGetBotLogic(ctx, s.svcCtx)
	return l.GetBot(in)
}

func (s *BotPlatformServer) ListBots(ctx context.Context, in *pb.ListBotsReq) (*pb.ListBotsResp, error) {
	l := botplatform.NewListBotsLogic(ctx, s.svcCtx)
	return l.ListBots(in)
}

func (s *BotPlatformServer) RotateSecret(ctx context.Context, in *pb.RotateSecretReq) (*pb.RotateSecretResp, error) {
	l := botplatform.NewRotateSecretLogic(ctx, s.svcCtx)
	return l.RotateSecret(in)
}

func (s *BotPlatformServer) HandleIncomingWebhook(ctx context.Context, in *pb.WebhookReq) (*pb.WebhookResp, error) {
	l := botplatform.NewHandleIncomingWebhookLogic(ctx, s.svcCtx)
	return l.HandleIncomingWebhook(in)
}

func (s *BotPlatformServer) IssueBotToken(ctx context.Context, in *pb.IssueBotTokenReq) (*pb.IssueBotTokenResp, error) {
	l := botplatform.NewIssueBotTokenLogic(ctx, s.svcCtx)
	return l.IssueBotToken(in)
}

func (s *BotPlatformServer) ValidateBotToken(ctx context.Context, in *pb.ValidateBotTokenReq) (*pb.ValidateBotTokenResp, error) {
	l := botplatform.NewValidateBotTokenLogic(ctx, s.svcCtx)
	return l.ValidateBotToken(in)
}

func (s *BotPlatformServer) BatchGetBots(ctx context.Context, in *pb.BatchGetBotsReq) (*pb.BatchGetBotsResp, error) {
	l := botplatform.NewBatchGetBotsLogic(ctx, s.svcCtx)
	return l.BatchGetBots(in)
}

func (s *BotPlatformServer) GetBotWebhookConfig(ctx context.Context, in *pb.GetBotWebhookConfigReq) (*pb.GetBotWebhookConfigResp, error) {
	l := botplatform.NewGetBotWebhookConfigLogic(ctx, s.svcCtx)
	return l.GetBotWebhookConfig(in)
}

// ========== MCP Server Management ==========

func (s *BotPlatformServer) CreateMcpServer(ctx context.Context, in *pb.CreateMcpServerReq) (*pb.McpServerInfo, error) {
	l := botplatform.NewCreateMcpServerLogic(ctx, s.svcCtx)
	return l.CreateMcpServer(in)
}

func (s *BotPlatformServer) UpdateMcpServer(ctx context.Context, in *pb.UpdateMcpServerReq) (*pb.McpServerInfo, error) {
	l := botplatform.NewUpdateMcpServerLogic(ctx, s.svcCtx)
	return l.UpdateMcpServer(in)
}

func (s *BotPlatformServer) DeleteMcpServer(ctx context.Context, in *pb.DeleteMcpServerReq) (*common.BaseResponse, error) {
	l := botplatform.NewDeleteMcpServerLogic(ctx, s.svcCtx)
	return l.DeleteMcpServer(in)
}

func (s *BotPlatformServer) GetMcpServer(ctx context.Context, in *pb.GetMcpServerReq) (*pb.McpServerInfo, error) {
	l := botplatform.NewGetMcpServerLogic(ctx, s.svcCtx)
	return l.GetMcpServer(in)
}

func (s *BotPlatformServer) ListMcpServers(ctx context.Context, in *pb.ListMcpServersReq) (*pb.ListMcpServersResp, error) {
	l := botplatform.NewListMcpServersLogic(ctx, s.svcCtx)
	return l.ListMcpServers(in)
}

// ========== Bot MCP Assignment ==========

func (s *BotPlatformServer) AssignMcpToBot(ctx context.Context, in *pb.AssignMcpToBotReq) (*common.BaseResponse, error) {
	l := botplatform.NewAssignMcpToBotLogic(ctx, s.svcCtx)
	return l.AssignMcpToBot(in)
}

func (s *BotPlatformServer) UnassignMcpFromBot(ctx context.Context, in *pb.UnassignMcpFromBotReq) (*common.BaseResponse, error) {
	l := botplatform.NewUnassignMcpFromBotLogic(ctx, s.svcCtx)
	return l.UnassignMcpFromBot(in)
}

func (s *BotPlatformServer) ListBotMcpServers(ctx context.Context, in *pb.ListBotMcpServersReq) (*pb.ListBotMcpServersResp, error) {
	l := botplatform.NewListBotMcpServersLogic(ctx, s.svcCtx)
	return l.ListBotMcpServers(in)
}

func (s *BotPlatformServer) UpdateBotMcpServer(ctx context.Context, in *pb.UpdateBotMcpServerReq) (*common.BaseResponse, error) {
	l := botplatform.NewUpdateBotMcpServerLogic(ctx, s.svcCtx)
	return l.UpdateBotMcpServer(in)
}

// ========== MCP Tool Discovery ==========

func (s *BotPlatformServer) DiscoverMcpTools(ctx context.Context, in *pb.DiscoverMcpToolsReq) (*pb.DiscoverMcpToolsResp, error) {
	l := botplatform.NewDiscoverMcpToolsLogic(ctx, s.svcCtx)
	return l.DiscoverMcpTools(in)
}

func (s *BotPlatformServer) ListMcpTools(ctx context.Context, in *pb.ListMcpToolsReq) (*pb.ListMcpToolsResp, error) {
	l := botplatform.NewListMcpToolsLogic(ctx, s.svcCtx)
	return l.ListMcpTools(in)
}
