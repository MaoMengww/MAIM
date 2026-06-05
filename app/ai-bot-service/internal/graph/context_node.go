package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/app/ai-bot-service/internal/model"
	"github.com/maomeng/aim/pkg/logx"
)

// BuildContextNode gathers all context before LLM inference.
type BuildContextNode struct {
	bot         *model.Bot
	convBot     *model.ConvBot
	msgClient   MsgClient
	resolver    *KnowledgeResolver
	memoryStore MemoryStore
	userNames   []UserNamesFunc
	logger      logx.Logger
}

// BuildContextResult is the output of building context.
type BuildContextResult struct {
	RenderedPrompt string            // system prompt
	Vars           map[string]string // template variables
	ContextMsgs    []*schema.Message // knowledge, memories, history
	KbSources      []KnowledgeSource
}

// NewBuildContextNode creates a new BuildContext node.
func NewBuildContextNode(bot *model.Bot, convBot *model.ConvBot,
	msgClient MsgClient, resolver *KnowledgeResolver,
	memoryStore MemoryStore, logger logx.Logger,
	userNames ...UserNamesFunc) *BuildContextNode {
	return &BuildContextNode{
		bot:         bot,
		convBot:     convBot,
		msgClient:   msgClient,
		resolver:    resolver,
		memoryStore: memoryStore,
		logger:      logger,
		userNames:   userNames,
	}
}

// Invoke loads history, knowledge, and memory, then renders the prompt.
func (n *BuildContextNode) Invoke(ctx context.Context, event *model.BotEvent) *BuildContextResult {
	vars := map[string]string{
		"botname":       n.bot.Name,
		"bot_name":      n.bot.Name,
		"username":      "",
		"user_name":     "",
		"user_language": "",
	}
	if event != nil && event.Sender != nil {
		vars["username"] = event.Sender.Username
		vars["user_name"] = event.Sender.Username
		vars["user_language"] = event.Sender.Language
	}

	systemPrompt := n.bot.SystemPrompt
	if systemPrompt == "" && n.bot.Persona != "" {
		systemPrompt = n.bot.Persona
	}
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant."
	}
	renderedPrompt := renderTemplate(systemPrompt, vars)
	if localeInstruction := promptLocaleInstruction(n.bot.Settings); localeInstruction != "" {
		renderedPrompt = fmt.Sprintf("%s\n\n%s", localeInstruction, renderedPrompt)
	}

	msgText := ""
	if event != nil && event.Message != nil {
		msgText = event.Message.Text
	}
	vars["message"] = msgText

	contextMsgs := make([]*schema.Message, 0, 3)
	history := n.loadHistory(ctx, event)
	knowledge, kbSources := n.loadKnowledge(ctx, msgText, history, event)
	if knowledge != "" {
		contextMsgs = append(contextMsgs, &schema.Message{Role: schema.Assistant, Content: knowledge})
	}
	if memories := n.loadMemories(ctx, msgText, event); memories != "" {
		contextMsgs = append(contextMsgs, &schema.Message{Role: schema.Assistant, Content: memories})
	}
	if len(history) > 0 {
		contextMsgs = append(contextMsgs, history...)
	}

	promptPreview := renderedPrompt
	if len(promptPreview) > 500 {
		promptPreview = promptPreview[:500] + "..."
	}
	n.withLogger(ctx).Infof("prompt assembled: bot_id=%d conv_id=%d prompt_len=%d prompt=%q",
		n.bot.ID, getConvID(event), len(renderedPrompt), promptPreview)

	return &BuildContextResult{
		RenderedPrompt: renderedPrompt,
		Vars:           vars,
		ContextMsgs:    contextMsgs,
		KbSources:      kbSources,
	}
}

func (n *BuildContextNode) withLogger(ctx context.Context) logx.Logger {
	if n.logger != nil {
		return n.logger.WithContext(ctx)
	}
	return logx.DefaultLogger().WithContext(ctx)
}

func (n *BuildContextNode) loadHistory(ctx context.Context, event *model.BotEvent) []*schema.Message {
	if n.msgClient == nil || event == nil || event.ConvID <= 0 {
		return nil
	}
	limit := n.bot.MaxContextMessages
	if limit <= 0 {
		limit = 10
	}
	msgs, err := n.msgClient.GetRecentMessages(ctx, event.ConvID, event.Sender.UserID, limit)
	if err != nil || len(msgs) == 0 {
		return nil
	}

	currentMsgID := int64(0)
	if event.Message != nil {
		currentMsgID = event.Message.MsgID
	}

	result := make([]*schema.Message, 0, len(msgs))
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if currentMsgID > 0 && m.MsgID == currentMsgID {
			continue
		}
		role := schema.User
		if m.SenderID == n.bot.ID {
			role = schema.Assistant
		}
		result = append(result, &schema.Message{
			Role:    role,
			Content: m.Content,
		})
	}
	return result
}

func (n *BuildContextNode) loadKnowledge(ctx context.Context, query string, history []*schema.Message, event *model.BotEvent) (string, []KnowledgeSource) {
	if !n.bot.EnableKnowledge || n.resolver == nil || query == "" {
		return "", nil
	}
	var convID int64
	if event != nil {
		convID = event.ConvID
	}
	historyText := ""
	if len(history) > 0 {
		var b strings.Builder
		for _, m := range history {
			role := "用户"
			if m.Role == schema.Assistant {
				role = "assistant"
			}
			b.WriteString(fmt.Sprintf("[%s]: %s\n", role, m.Content))
		}
		historyText = b.String()
	}
	return n.resolver.Query(ctx, query, n.bot.ID, convID, n.bot.ModelID, n.bot.ModelName, historyText)
}

// renderTemplate replaces {key} placeholders with values.
func renderTemplate(tpl string, vars map[string]string) string {
	if tpl == "" {
		return ""
	}
	result := tpl
	for k, v := range vars {
		result = strings.ReplaceAll(result, "{"+k+"}", v)
	}
	return result
}

func getConvID(event *model.BotEvent) int64 {
	if event != nil {
		return event.ConvID
	}
	return 0
}

var localeInstructions = map[string]string{
	"en-US": "Please respond in English unless the user explicitly requests otherwise.",
	"zh-CN": "请用中文回复，除非用户明确要求使用其他语言。",
	"ja-JP": "特に明示されない限り、日本語で応答してください。",
	"ko-KR": "사용자가 명시적으로 요청하지 않는 한 한국어로 응답하십시오.",
}

// LocalePrompt returns a language instruction string for the given locale.
func LocalePrompt(locale string) string {
	return localeInstructions[locale]
}

func promptLocaleInstruction(settings map[string]any) string {
	if settings == nil {
		return ""
	}
	raw, ok := settings["prompt_locale"]
	if !ok {
		return ""
	}
	locale, ok := raw.(string)
	if !ok {
		return ""
	}
	return LocalePrompt(locale)
}

func (n *BuildContextNode) loadMemories(ctx context.Context, query string, event *model.BotEvent) string {
	memoryLimit := n.bot.MemoryLimit
	if memoryLimit <= 0 {
		memoryLimit = 5
	}
	if n.memoryStore == nil || event == nil || event.Sender == nil || query == "" {
		return ""
	}
	items, err := n.memoryStore.Retrieve(ctx, n.bot.ID, event.Sender.UserID, query, memoryLimit)
	if err != nil || len(items) == 0 {
		return ""
	}
	return FormatMemories(items)
}
