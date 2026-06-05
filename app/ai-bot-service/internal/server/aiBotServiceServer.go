package server

import (
	"context"
	"io"
	"strconv"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/maomeng/aim/app/ai-bot-service/internal/client"
	"github.com/maomeng/aim/app/ai-bot-service/internal/component"
	"github.com/maomeng/aim/app/ai-bot-service/internal/graph"
	"github.com/maomeng/aim/app/ai-bot-service/internal/memory"
	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	"github.com/maomeng/aim/app/ai-bot-service/internal/repo"
	"github.com/maomeng/aim/app/ai-bot-service/internal/svc"
	"github.com/maomeng/aim/app/ai-bot-service/pb/aibot"
	botplatform "github.com/maomeng/aim/app/bot-platform/pb/botplatform"
	"github.com/maomeng/aim/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

type AiBotServiceServer struct {
	svcCtx *svc.ServiceContext
	aibot.UnimplementedAiBotServiceServer
}

func NewAiBotServiceServer(svcCtx *svc.ServiceContext) *AiBotServiceServer {
	return &AiBotServiceServer{svcCtx: svcCtx}
}

func buildRuntimeEvent(req *aibot.StreamChatReq, userID int64, username string, language string) *model.BotEvent {
	return &model.BotEvent{
		BotID:  req.BotId,
		ConvID: req.ConvId,
		Sender: &model.EventSender{UserID: userID, Username: username, Language: language},
		Message: &model.EventMessage{
			Text:         req.Message,
			ReplyToMsgID: req.ReplyToMsgId,
		},
	}
}

func extractChatCaller(ctx context.Context) (int64, string, string) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0, "", ""
	}
	var userID int64
	if vals := md.Get("x-user-id"); len(vals) > 0 {
		if v, err := strconv.ParseInt(vals[0], 10, 64); err == nil {
			userID = v
		}
	}
	username := ""
	if vals := md.Get("x-username"); len(vals) > 0 {
		username = vals[0]
	}
	language := ""
	if vals := md.Get("x-user-language"); len(vals) > 0 {
		language = vals[0]
	}
	return userID, username, language
}

// StreamChat implements single-chat streaming conversation using ReAct agent.
func (s *AiBotServiceServer) StreamChat(req *aibot.StreamChatReq, stream grpc.ServerStreamingServer[aibot.StreamChatResp]) error {
	ctx := stream.Context()
	logger := s.svcCtx.Logger.WithContext(ctx)

	botRepo := repo.NewBotRepo(s.svcCtx.DB, botplatform.NewBotPlatformClient(s.svcCtx.BotPlatformConn.Conn()))
	bot, err := botRepo.FindByID(ctx, req.BotId)
	if err != nil {
		logger.Errorf("bot not found: bot_id=%d error=%v", req.BotId, err)
		return errors.New(errors.CodeNotFound, "bot not found")
	}

	llmClient := client.NewLlmGatewayClient(s.svcCtx.LlmGatewayConn)
	msgClient := client.NewMessageClient(s.svcCtx.MessageSvcConn)
	einoChatModel := llmClient.NewEinoChatModel(bot.ModelID, bot.ModelName, bot.OwnerID)

	// MCP tools
	var mcpTools []tool.BaseTool
	if mcpConfigs := loadBotMcpServers(ctx, botRepo, req.BotId); len(mcpConfigs) > 0 {
		if tools, toolErr := component.GetMCPServerTools(ctx, mcpConfigs); toolErr == nil {
			mcpTools = tools
		}
	}

	userID, _, language := extractChatCaller(ctx)

	// Track used tools
	var usedTools []string
	toolMw := compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(c context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				usedTools = append(usedTools, input.Name)
				return next(c, input)
			}
		},
	}
	// Build system prompt with locale instruction
	systemPrompt := bot.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant."
	}
	if loc := graph.LocalePrompt(language); loc != "" {
		systemPrompt = loc + "\n\n" + systemPrompt
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: einoChatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               mcpTools,
			ToolCallMiddlewares: []compose.ToolMiddleware{toolMw},
		},
		MessageModifier: func(_ context.Context, msgs []*schema.Message) []*schema.Message {
			return append([]*schema.Message{
				{Role: schema.System, Content: systemPrompt},
			}, msgs...)
		},
		MaxStep: bot.MaxStep,
	})
	if err != nil {
		logger.Errorf("create react agent failed: bot_id=%d error=%v", req.BotId, err)
		return errors.Wrap(errors.CodeRPCError, "create agent failed", err)
	}

	input := []*schema.Message{
		{Role: schema.User, Content: req.Message},
	}

	if bot.StreamingEnabled {
		sr, streamErr := agent.Stream(ctx, input)
		if streamErr != nil {
			logger.Errorf("agent stream failed: bot_id=%d error=%v", req.BotId, streamErr)
			return errors.Wrap(errors.CodeRPCError, "agent stream failed", streamErr)
		}

		var fullText string
		for {
			chunk, recvErr := sr.Recv()
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				logger.Errorf("agent stream recv failed: bot_id=%d error=%v", req.BotId, recvErr)
				break
			}
			if chunk != nil && chunk.Content != "" {
				fullText += chunk.Content
				_ = stream.Send(&aibot.StreamChatResp{
					Type:    "chunk",
					Content: chunk.Content,
					ConvId:  req.ConvId,
				})
			}
		}

		s.triggerMemoryExtraction(ctx, bot, userID, req.Message, fullText)
		if fullText != "" {
			if _, err := msgClient.SendBotReply(ctx, req.BotId, req.ConvId, fullText, req.ReplyToMsgId); err != nil {
				logger.Errorf("send bot reply failed: bot_id=%d error=%v", req.BotId, err)
			}
		}
		_ = stream.Send(&aibot.StreamChatResp{
			Type:    "done",
			Content: fullText,
			ConvId:  req.ConvId,
		})
		return nil
	}

	msg, genErr := agent.Generate(ctx, input)
	if genErr != nil {
		logger.Errorf("agent generate failed: bot_id=%d error=%v", req.BotId, genErr)
		_ = stream.Send(&aibot.StreamChatResp{Type: "error", Content: "execution failed: " + genErr.Error()})
		return nil
	}

	s.triggerMemoryExtraction(ctx, bot, userID, req.Message, msg.Content)
	if msg.Content != "" {
		if _, err := msgClient.SendBotReply(ctx, req.BotId, req.ConvId, msg.Content, req.ReplyToMsgId); err != nil {
			logger.Errorf("send bot reply failed: bot_id=%d error=%v", req.BotId, err)
		}
	}
	return stream.Send(&aibot.StreamChatResp{Type: "done", Content: msg.Content, ConvId: req.ConvId})
}

func loadBotMcpServers(ctx context.Context, botRepo *repo.BotRepo, botID int64) []model.MCPServerConfig {
	servers, err := botRepo.ListBotMcpServers(ctx, botID)
	if err != nil || len(servers) == 0 {
		return nil
	}
	configs := make([]model.MCPServerConfig, 0, len(servers))
	for _, srv := range servers {
		timeoutMs := 0
		if srv.AdvancedConfig != nil {
			timeoutMs = srv.AdvancedConfig.Timeout * 1000
		}
		configs = append(configs, model.MCPServerConfig{
			Name:           srv.Name,
			Transport:      srv.Transport,
			URL:            srv.URL,
			AuthConfig:     srv.AuthConfig,
			AdvancedConfig: srv.AdvancedConfig,
			TimeoutMs:      timeoutMs,
		})
	}
	return configs
}

func (s *AiBotServiceServer) triggerMemoryExtraction(ctx context.Context, bot *model.Bot, userID int64, userMsg string, botResponse string) {
	if userMsg == "" || botResponse == "" {
		return
	}
	memRepo := memory.NewPgRepo(s.svcCtx.DB)
	llmClient := client.NewLlmGatewayClient(s.svcCtx.LlmGatewayConn)
	einoChatModel := llmClient.NewEinoChatModel(bot.ModelID, bot.ModelName, bot.OwnerID)
	extractor := memory.NewExtractor(einoChatModel, memory.ExtractorConfig{
		Temperature: 0.3,
	})
	dialog := []memory.Message{
		{Role: "user", Content: userMsg},
		{Role: "assistant", Content: botResponse},
	}
	mgr := memory.NewManager(s.svcCtx.Logger, memRepo, extractor)
	mgr.CreateAsync(ctx, bot.ID, userID, dialog)
}

func (s *AiBotServiceServer) GetUserMemories(ctx context.Context, req *aibot.GetUserMemoriesReq) (*aibot.GetUserMemoriesRsp, error) {
	memRepo := memory.NewPgRepo(s.svcCtx.DB)
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	mems, err := memRepo.FindByUser(ctx, req.BotId, req.UserId, limit)
	if err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "query memories failed", err)
	}

	items := make([]*aibot.MemoryItem, len(mems))
	for i, m := range mems {
		items[i] = &aibot.MemoryItem{
			Id:         m.ID,
			Content:    m.Content,
			MemoryType: m.MemoryType,
			Category:   m.Category,
			Importance: m.Importance,
			CreatedAt:  m.CreatedAt.Format("2006-01-02T15:04:05Z"),
			FinalScore: m.FinalScore,
		}
	}
	return &aibot.GetUserMemoriesRsp{Items: items}, nil
}

func (s *AiBotServiceServer) ForgetMemory(ctx context.Context, req *aibot.ForgetMemoryReq) (*emptypb.Empty, error) {
	memRepo := memory.NewPgRepo(s.svcCtx.DB)

	mem, err := memRepo.FindByID(ctx, req.MemoryId)
	if err != nil {
		return nil, errors.New(errors.CodeNotFound, "memory not found")
	}
	if mem.BotID != req.BotId || mem.UserID != req.UserId {
		return nil, errors.New(errors.CodeForbidden, "not the owner of this memory")
	}

	if err := memRepo.Delete(ctx, req.MemoryId); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "delete memory failed", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *AiBotServiceServer) ClearUserMemories(ctx context.Context, req *aibot.ClearUserMemoriesReq) (*emptypb.Empty, error) {
	memRepo := memory.NewPgRepo(s.svcCtx.DB)
	if err := memRepo.DeleteByUser(ctx, req.BotId, req.UserId); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "clear memories failed", err)
	}
	return &emptypb.Empty{}, nil
}
