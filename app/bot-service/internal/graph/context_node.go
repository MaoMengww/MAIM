package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/app/bot-service/internal/model"
	"github.com/maomeng/aim/pkg/logx"
	"google.golang.org/grpc/metadata"
)

// BuildContextNode gathers all context before LLM inference.
type BuildContextNode struct {
	bot       *model.Bot
	convBot   *model.ConvBot
	msgClient MsgClient
	resolver  *KnowledgeResolver
	userNames []UserNamesFunc
	logger    logx.Logger
}

// BuildContextResult is the output of building context.
type BuildContextResult struct {
	RenderedPrompt string            // system prompt
	Vars           map[string]string // template variables
	ContextMsgs    []*schema.Message // knowledge and history
	KbSources      []KnowledgeSource
}

// NewBuildContextNode creates a new BuildContext node.
func NewBuildContextNode(bot *model.Bot, convBot *model.ConvBot,
	msgClient MsgClient, resolver *KnowledgeResolver,
	logger logx.Logger,
	userNames ...UserNamesFunc) *BuildContextNode {
	return &BuildContextNode{
		bot:       bot,
		convBot:   convBot,
		msgClient: msgClient,
		resolver:  resolver,
		logger:    logger,
		userNames: userNames,
	}
}

// Invoke loads explicitly granted history and knowledge, then renders the prompt.
func (n *BuildContextNode) Invoke(ctx context.Context, event *model.BotEvent) (*BuildContextResult, error) {
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
	history, err := n.loadHistory(ctx, event)
	if err != nil {
		return nil, err
	}
	knowledge, kbSources, err := n.loadKnowledge(ctx, msgText, event)
	if err != nil {
		return nil, err
	}
	if knowledge != "" {
		contextMsgs = append(contextMsgs, &schema.Message{Role: schema.Assistant, Content: knowledge})
	}
	if len(history) > 0 {
		contextMsgs = append(contextMsgs, history...)
	}

	promptPreview := renderedPrompt
	if len(promptPreview) > 500 {
		promptPreview = promptPreview[:500] + "..."
	}
	n.withLogger(ctx).Infof("prompt assembled: bot_id=%s conv_id=%s prompt_len=%d prompt=%q",
		n.bot.ID, getConvID(event), len(renderedPrompt), promptPreview)

	return &BuildContextResult{
		RenderedPrompt: renderedPrompt,
		Vars:           vars,
		ContextMsgs:    contextMsgs,
		KbSources:      kbSources,
	}, nil
}

func (n *BuildContextNode) withLogger(ctx context.Context) logx.Logger {
	if n.logger != nil {
		return n.logger.WithContext(ctx)
	}
	return logx.DefaultLogger().WithContext(ctx)
}

func (n *BuildContextNode) loadHistory(ctx context.Context, event *model.BotEvent) ([]*schema.Message, error) {
	if event == nil || event.HistoryGrantID == "" {
		return nil, nil
	}
	if n.msgClient == nil || n.convBot == nil || event.BindingID != n.convBot.ID || event.BotID != n.convBot.BotID || event.ConvID != n.convBot.ConvID {
		return nil, fmt.Errorf("history requires the captured bot binding")
	}
	limit := n.bot.MaxContextMessages
	if limit <= 0 {
		limit = 10
	}
	msgs, err := n.msgClient.GetGrantedMessages(ctx, n.bot.ID, event.ConvID, n.convBot.ID, event.HistoryGrantID, limit)
	if err != nil {
		return nil, err
	}

	currentMsgID := ""
	if event.Message != nil {
		currentMsgID = event.Message.MsgID
	}

	result := make([]*schema.Message, 0, len(msgs))
	totalTokens := 0
	for _, m := range msgs {
		if currentMsgID != "" && m.MsgID == currentMsgID {
			continue
		}
		// History is cited source material, not this execution's assistant trajectory.
		content := fmt.Sprintf("[%s][sender_type=%s][sender_id=%s][%s]: %s", formatMessageTime(m.CreatedAt), m.SenderType, messageSenderID(m), messageSenderName(m), m.Content)
		if n.bot.MaxContextTokens > 0 {
			tokens := estimateTokens(content)
			if totalTokens+tokens > n.bot.MaxContextTokens {
				continue
			}
			totalTokens += tokens
		}
		result = append(result, &schema.Message{
			Role:    schema.User,
			Content: content,
		})
	}
	slices.Reverse(result)
	return result, nil
}

func (n *BuildContextNode) loadKnowledge(ctx context.Context, query string, event *model.BotEvent) (string, []KnowledgeSource, error) {
	if !n.bot.EnableKnowledge || n.resolver == nil || query == "" || event == nil || event.Sender == nil || event.Sender.UserID == nil {
		return "", nil, nil
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-user-id")
	md.Set("user-id", *event.Sender.UserID)
	ctx = metadata.NewOutgoingContext(ctx, md)
	return n.resolver.Query(ctx, query, n.bot.ID, event.ConvID)
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

func getConvID(event *model.BotEvent) string {
	if event != nil {
		return event.ConvID
	}
	return ""
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

func promptLocaleInstruction(data []byte) string {
	var settings map[string]any
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
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
