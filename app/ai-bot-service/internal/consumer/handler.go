package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/maomeng/aim/app/ai-bot-service/internal/client"
	"github.com/maomeng/aim/app/ai-bot-service/internal/component"
	"github.com/maomeng/aim/app/ai-bot-service/internal/graph"
	"github.com/maomeng/aim/app/ai-bot-service/internal/memory"
	"github.com/maomeng/aim/app/ai-bot-service/internal/metrics"
	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	"github.com/maomeng/aim/app/ai-bot-service/internal/repo"
	"github.com/maomeng/aim/app/ai-bot-service/internal/stream"
	"github.com/maomeng/aim/pkg/logx"
)

type Handler struct {
	logger        logx.Logger
	botRepo       *repo.BotRepo
	convBotRepo   *repo.ConvBotRepo
	llmClient     *client.LlmGatewayClient
	retriever     component.Retriever
	memoryStore   graph.MemoryStore
	memoryManager *memory.Manager
	msgClient     graph.MsgClient
	kbClient      graph.KbClient
	convClient    interface {
		GetConversationMembers(ctx context.Context, convID int64) ([]int64, error)
	}
	dedup     *Dedup
	userNames graph.UserNamesFunc
	wsClient  stream.WsGatewayClient
}

func NewHandler(
	logger logx.Logger,
	botRepo *repo.BotRepo,
	convBotRepo *repo.ConvBotRepo,
	llmClient *client.LlmGatewayClient,
	retriever component.Retriever,
	memoryStore graph.MemoryStore,
	memoryManager *memory.Manager,
	msgClient graph.MsgClient,
	kbClient graph.KbClient,
	convClient interface {
		GetConversationMembers(ctx context.Context, convID int64) ([]int64, error)
	},
	dedup *Dedup,
	wsClient stream.WsGatewayClient,
	userNames ...graph.UserNamesFunc,
) *Handler {
	var un graph.UserNamesFunc
	if len(userNames) > 0 {
		un = userNames[0]
	}
	return &Handler{
		logger:        logger,
		botRepo:       botRepo,
		convBotRepo:   convBotRepo,
		llmClient:     llmClient,
		retriever:     retriever,
		memoryStore:   memoryStore,
		memoryManager: memoryManager,
		msgClient:     msgClient,
		kbClient:      kbClient,
		convClient:    convClient,
		dedup:         dedup,
		wsClient:      wsClient,
		userNames:     un,
	}
}

func (h *Handler) Handle(ctx context.Context, raw []byte) error {
	event, msgID, err := parseEvent(raw)
	if err != nil {
		return fmt.Errorf("parse event: %w", err)
	}

	if event.BotID == 0 && event.ConvID != 0 && h.convBotRepo != nil {
		bots, cbErr := h.convBotRepo.FindByConv(ctx, event.ConvID)
		if cbErr == nil && len(bots) > 0 {
			event.BotID = bots[0].BotID
		}
	}

	if event.BotID == 0 {
		return nil
	}

	if event.Sender != nil && event.Sender.UserID == event.BotID {
		return nil
	}

	eventID := fmt.Sprintf("%d_%s_%d", msgID, event.EventType, event.BotID)
	isDup, err := h.dedup.IsDuplicate(ctx, eventID)
	if err != nil {
		return fmt.Errorf("dedup check failed: %w", err)
	}
	if isDup {
		return nil
	}

	bot, convBot, err := h.botRepo.FindByIDWithConvBot(ctx, event.BotID, event.ConvID)
	if err != nil || bot == nil {
		return fmt.Errorf("bot/conv not found: %w", err)
	}

	if !shouldRespond(event, bot) {
		return nil
	}

	var mcpConfigs []model.MCPServerConfig
	if mcpServers, mcpErr := h.botRepo.ListBotMcpServers(ctx, event.BotID); mcpErr == nil {
		for _, srv := range mcpServers {
			timeoutMs := 0
			if srv.AdvancedConfig != nil {
				timeoutMs = srv.AdvancedConfig.Timeout * 1000
			}
			mcpConfigs = append(mcpConfigs, model.MCPServerConfig{
				Name:           srv.Name,
				Transport:      srv.Transport,
				URL:            srv.URL,
				AuthConfig:     srv.AuthConfig,
				AdvancedConfig: srv.AdvancedConfig,
				TimeoutMs:      timeoutMs,
			})
		}
	}

	botIDStr := strconv.FormatInt(event.BotID, 10)
	start := time.Now()

	// ---- context building ----
	resolver := graph.NewKnowledgeResolver(h.kbClient)
	ctxNode := graph.NewBuildContextNode(bot, convBot, h.msgClient, resolver, h.memoryStore, h.logger, h.userNames)
	ctxResult := ctxNode.Invoke(ctx, event)

	// ---- MCP tools ----
	var mcpTools []tool.BaseTool
	if len(mcpConfigs) > 0 {
		tools, toolErr := component.GetMCPServerTools(ctx, mcpConfigs)
		if toolErr != nil {
			h.logger.WithContext(ctx).Errorf("mcp tools failed: bot_id=%d error=%v", event.BotID, toolErr)
		} else {
			mcpTools = tools
			h.logger.WithContext(ctx).Infof("mcp tools loaded: bot_id=%d count=%d", event.BotID, len(tools))
		}
	}

	// track used tools
	var usedTools []string
	toolMiddleware := compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(c context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				usedTools = append(usedTools, input.Name)
				return next(c, input)
			}
		},
	}

	einoChatModel := h.llmClient.NewEinoChatModel(bot.ModelID, bot.ModelName, bot.OwnerID)

	msgText := ""
	if event != nil && event.Message != nil {
		msgText = event.Message.Text
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: einoChatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               mcpTools,
			ToolCallMiddlewares: []compose.ToolMiddleware{toolMiddleware},
		},
		MessageModifier: func(c context.Context, msgs []*schema.Message) []*schema.Message {
			result := make([]*schema.Message, 0, 2+len(ctxResult.ContextMsgs)+len(msgs))
			if ctxResult.RenderedPrompt != "" {
				result = append(result, &schema.Message{Role: schema.System, Content: ctxResult.RenderedPrompt})
			}
			result = append(result, ctxResult.ContextMsgs...)
			result = append(result, msgs...)
			return result
		},
		MaxStep: bot.MaxStep,
	})
	if err != nil {
		return fmt.Errorf("create react agent: %w", err)
	}

	if bot.StreamingEnabled && h.wsClient != nil {
		replyToMsgID := int64(0)
		if event.Message != nil {
			replyToMsgID = event.Message.MsgID
		}
		pusher := stream.NewWSPusher(h.wsClient, event.ConvID, event.BotID, replyToMsgID)

		stream, streamErr := agent.Stream(ctx, []*schema.Message{
			{Role: schema.User, Content: msgText},
		})
		if streamErr != nil {
			metrics.BotRequestsTotal.Inc(botIDStr, "error")
			metrics.BotResponseSeconds.Observe(time.Since(start).Seconds(), botIDStr)
			h.logger.WithContext(ctx).Errorf("agent stream failed: event_id=%s duration=%.3fs error=%v", eventID, time.Since(start).Seconds(), streamErr)

			fallbackText := h.tryDirectGenerate(ctx, bot, ctxResult, msgText)
			if fallbackText == "" {
				h.logger.WithContext(ctx).Infof("direct generate fallback also failed: event_id=%s", eventID)
				fallbackText = fallbackForLanguage(event.Sender)
			}
			replyTo := int64(0)
			if event.Message != nil {
				replyTo = event.Message.MsgID
			}
			if _, fbErr := h.msgClient.SendBotReply(ctx, event.BotID, event.ConvID, fallbackText, replyTo); fbErr != nil {
				h.logger.WithContext(ctx).Errorf("send fallback failed: event_id=%s error=%v", eventID, fbErr)
			}
			return fmt.Errorf("agent stream: %w", streamErr)
		}

		var fullText string
		for {
			chunk, recvErr := stream.Recv()
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				h.logger.WithContext(ctx).Errorf("agent stream recv failed: event_id=%s error=%v", eventID, recvErr)
				break
			}
			if chunk != nil && chunk.Content != "" {
				fullText += chunk.Content
				if err := pusher.Send(&model.StreamChunk{
					Type:    "chunk",
					Content: chunk.Content,
					ConvID:  event.ConvID,
				}); err != nil {
					return err
				}
			}
		}

		metrics.BotRequestsTotal.Inc(botIDStr, "success")
		metrics.BotResponseSeconds.Observe(time.Since(start).Seconds(), botIDStr)

		if fullText == "" {
			return nil
		}

		rawPayload := buildRawPayload(ctxResult.KbSources, usedTools)

		// Send knowledge sources
		if len(ctxResult.KbSources) > 0 {
			sourcesJSON, _ := json.Marshal(ctxResult.KbSources)
			_ = pusher.Send(&model.StreamChunk{
				Type:    "sources",
				Content: string(sourcesJSON),
				ConvID:  event.ConvID,
			})
		}

		// Send tool_used notification
		if len(usedTools) > 0 {
			_ = pusher.Send(&model.StreamChunk{
				Type:    "tool_used",
				Content: strings.Join(usedTools, ","),
				ConvID:  event.ConvID,
			})
		}

		respMsgID, sendErr := h.msgClient.SendBotReply(ctx, event.BotID, event.ConvID, fullText, replyToMsgID, rawPayload)
		if sendErr != nil {
			h.logger.WithContext(ctx).Errorf("send reply failed: event_id=%s error=%v", eventID, sendErr)
			return fmt.Errorf("send bot reply: %w", sendErr)
		}
		_ = pusher.Send(&model.StreamChunk{
			Type:      "done",
			Content:   fullText,
			MessageID: strconv.FormatInt(respMsgID, 10),
			ConvID:    event.ConvID,
		})

		h.logger.WithContext(ctx).Infof("stream reply sent: event_id=%s bot_id=%d conv_id=%d msg_id=%d", eventID, event.BotID, event.ConvID, respMsgID)
		metrics.BotReplyMessagesTotal.Inc(botIDStr)
		if event.Message != nil && event.Sender != nil {
			h.triggerMemoryExtraction(ctx, bot, event.Sender.UserID, event.Message.Text, fullText)
		}
		return nil
	}

	// Non-streaming path
	msg, genErr := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: msgText},
	})
	duration := time.Since(start).Seconds()
	if genErr != nil {
		metrics.BotRequestsTotal.Inc(botIDStr, "error")
		metrics.BotResponseSeconds.Observe(duration, botIDStr)
		h.logger.WithContext(ctx).Errorf("agent generate failed: event_id=%s duration=%.3fs error=%v", eventID, duration, genErr)
		fallbackText := h.tryDirectGenerate(ctx, bot, ctxResult, msgText)
		if fallbackText == "" {
			h.logger.WithContext(ctx).Infof("direct generate fallback also failed: event_id=%s", eventID)
			fallbackText = fallbackForLanguage(event.Sender)
		}
		replyTo := int64(0)
		if event.Message != nil {
			replyTo = event.Message.MsgID
		}
		if _, fbErr := h.msgClient.SendBotReply(ctx, event.BotID, event.ConvID, fallbackText, replyTo); fbErr != nil {
			h.logger.WithContext(ctx).Errorf("send fallback failed: event_id=%s error=%v", eventID, fbErr)
		}
		return fmt.Errorf("agent generate: %w", genErr)
	}

	metrics.BotRequestsTotal.Inc(botIDStr, "success")
	metrics.BotResponseSeconds.Observe(duration, botIDStr)

	finalText := msg.Content
	if finalText == "" {
		return nil
	}

	replyTo := int64(0)
	if event.Message != nil {
		replyTo = event.Message.MsgID
	}
	rawPayload := buildRawPayload(ctxResult.KbSources, usedTools)

	if _, sendErr := h.msgClient.SendBotReply(ctx, event.BotID, event.ConvID, finalText, replyTo, rawPayload); sendErr != nil {
		h.logger.WithContext(ctx).Errorf("send reply failed: event_id=%s error=%v", eventID, sendErr)
		return fmt.Errorf("send bot reply: %w", sendErr)
	}

	h.logger.WithContext(ctx).Infof("reply sent: event_id=%s bot_id=%d conv_id=%d", eventID, event.BotID, event.ConvID)
	metrics.BotReplyMessagesTotal.Inc(botIDStr)
	if event.Message != nil && event.Sender != nil {
		h.triggerMemoryExtraction(ctx, bot, event.Sender.UserID, event.Message.Text, finalText)
	}
	return nil
}

// tryDirectGenerate calls the model without tools as a fallback when ReAct agent fails.
func (h *Handler) tryDirectGenerate(ctx context.Context, bot *model.Bot, ctxResult *graph.BuildContextResult, msgText string) string {
	if msgText == "" {
		return ""
	}
	chatModel := h.llmClient.NewEinoChatModel(bot.ModelID, bot.ModelName, bot.OwnerID)
	msgs := make([]*schema.Message, 0, 2+len(ctxResult.ContextMsgs))
	if ctxResult.RenderedPrompt != "" {
		msgs = append(msgs, &schema.Message{Role: schema.System, Content: ctxResult.RenderedPrompt})
	}
	msgs = append(msgs, ctxResult.ContextMsgs...)
	msgs = append(msgs, &schema.Message{Role: schema.User, Content: msgText})
	result, err := chatModel.Generate(ctx, msgs)
	if err != nil || result == nil {
		return ""
	}
	return result.Content
}
func (h *Handler) triggerMemoryExtraction(ctx context.Context, bot *model.Bot, userID int64, userMsg, botResponse string) {
	if h.memoryManager == nil || h.llmClient == nil || userMsg == "" || botResponse == "" {
		return
	}
	if bot.MemoryModelID <= 0 {
		return
	}
	einoChatModel := h.llmClient.NewEinoChatModel(bot.MemoryModelID, bot.MemoryModelName, bot.OwnerID)
	extractor := memory.NewExtractor(einoChatModel, memory.ExtractorConfig{Temperature: 0.3})
	h.memoryManager.WithExtractor(extractor).RememberAsync(ctx, memory.ExtractInput{
		BotID:            bot.ID,
		UserID:           userID,
		Message:          userMsg,
		SentAt:           time.Now(),
		OwnerID:          bot.OwnerID,
		EmbeddingModelID: bot.MemoryEmbeddingModelID,
		MemoryModelID:    bot.MemoryModelID,
		MemoryModelName:  bot.MemoryModelName,
	})
}

func buildRawPayload(kbSources []graph.KnowledgeSource, usedTools []string) string {
	if len(kbSources) == 0 && len(usedTools) == 0 {
		return ""
	}
	payload := map[string]any{"kb_sources": kbSources}
	if len(usedTools) > 0 {
		payload["tool_names"] = usedTools
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseEvent parses a Kafka message.created event, handling both nested (BotEvent)
// and flat formats.
func parseEvent(raw []byte) (*model.BotEvent, int64, error) {
	var event model.BotEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, 0, fmt.Errorf("json unmarshal: %w", err)
	}

	if event.Message != nil {
		return &event, event.Message.MsgID, nil
	}

	var flat map[string]any
	if err := json.Unmarshal(raw, &flat); err != nil {
		return &event, 0, nil
	}

	event.EventType = "message.created"

	if v, ok := flat["conv_id"].(float64); ok {
		event.ConvID = int64(v)
	}

	var msgID int64
	if v, ok := flat["message_id"].(float64); ok {
		msgID = int64(v)
	}
	if event.Message == nil {
		event.Message = &model.EventMessage{}
	}
	event.Message.MsgID = msgID
	if v, ok := flat["msg_type"].(float64); ok {
		event.Message.MsgType = int32(v)
	}
	if v, ok := flat["reply_to_msg_id"].(float64); ok {
		event.Message.ReplyToMsgID = int64(v)
	}

	if content, ok := flat["content"].(map[string]any); ok {
		if text, ok := content["text"].(string); ok {
			event.Message.Text = text
		}
		if mentions, ok := content["mention_user_ids"]; ok {
			switch m := mentions.(type) {
			case []any:
				for _, id := range m {
					if f, ok := id.(float64); ok {
						event.MentionedUserIDs = append(event.MentionedUserIDs, int64(f))
					}
				}
			}
		}
	}

	if v, ok := flat["sender_id"].(float64); ok {
		event.Sender = &model.EventSender{UserID: int64(v)}
	}
	if v, ok := flat["sender_name"].(string); ok && event.Sender != nil {
		event.Sender.Username = v
	}

	if v, ok := flat["bot_id"].(float64); ok {
		event.BotID = int64(v)
	}

	return &event, msgID, nil
}

// fallbackForLanguage returns a fallback message in the user's language.
func fallbackForLanguage(sender *model.EventSender) string {
	lang := ""
	if sender != nil {
		lang = sender.Language
	}
	switch lang {
	case "zh-CN":
		return "抱歉，我暂时无法回答，请稍后再试。"
	case "ja-JP":
		return "申し訳ありません。一時的に回答できません。後でもう一度お試しください。"
	case "ko-KR":
		return "죄송합니다. 지금은 답변할 수 없습니다. 나중에 다시 시도해주세요."
	default:
		return "Sorry, I am unable to answer right now. Please try again later."
	}
}

func shouldRespond(event *model.BotEvent, bot *model.Bot) bool {
	if bot == nil || len(bot.ResponseTriggers) == 0 {
		return false
	}
	for _, trigger := range bot.ResponseTriggers {
		switch {
		case trigger == "always":
			return true
		case trigger == "mention":
			if len(event.MentionedUserIDs) > 0 {
				return true
			}
		case strings.HasPrefix(trigger, "keyword:"):
			keyword := strings.TrimPrefix(trigger, "keyword:")
			if event.Message != nil && strings.Contains(event.Message.Text, keyword) {
				return true
			}
		case strings.HasPrefix(trigger, "event:"):
			eventType := strings.TrimPrefix(trigger, "event:")
			if event.EventType == eventType {
				return true
			}
		}
	}
	return false
}
