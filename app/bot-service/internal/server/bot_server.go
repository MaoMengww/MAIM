package server

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/logic/bot"
	"github.com/maomeng/aim/app/bot-service/internal/svc"
	pb "github.com/maomeng/aim/app/bot-service/pb/bot"
	msgpb "github.com/maomeng/aim/app/message-service/pb/message"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/interceptor"
	common "github.com/maomeng/aim/pkg/pb/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type BotServer struct {
	svcCtx  *svc.ServiceContext
	runtime *RuntimeServer
	pb.UnimplementedBotServiceServer
}

func NewBotServer(svcCtx *svc.ServiceContext) *BotServer {
	return &BotServer{svcCtx: svcCtx, runtime: NewRuntimeServer(svcCtx)}
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

func runtimeUserID(ctx context.Context) string {
	if id, ok := ctx.Value(interceptor.ContextKeyUserID).(string); ok && identity.Validate(id) == nil {
		return id
	}
	md, _ := metadata.FromIncomingContext(ctx)
	ids := md.Get(consts.MetadataKeyUserID)
	if len(ids) == 1 && identity.Validate(ids[0]) == nil {
		return ids[0]
	}
	return ""
}

func userCallContext(ctx context.Context, userID string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(consts.MetadataKeyUserID, userID)
	return metadata.NewOutgoingContext(ctx, md)
}

func requireConversation(ctx context.Context, svcCtx *svc.ServiceContext, userID, convID string) error {
	if identity.Validate(userID) != nil {
		return errors.New(errors.CodeUnauthorized, "missing authenticated user")
	}
	if identity.Validate(convID) != nil {
		return errors.New(errors.CodeInvalidParam, "invalid conversation identity")
	}
	cli := msgpb.NewMessageServiceClient(svcCtx.MessageSvcConn.Conn())
	_, err := cli.GetMembers(userCallContext(ctx, userID), &msgpb.GetMembersReq{
		ConversationId: convID,
		UserId:         &userID,
		Pagination:     &common.Pagination{Page: 1, PageSize: 1},
	})
	return err
}

func (s *BotServer) StreamChat(req *pb.StreamChatReq, stream grpc.ServerStreamingServer[pb.StreamChatResp]) error {
	return s.runtime.StreamChat(req, stream)
}
