package convtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/pkg/logx"
)

const summarizePrompt = `You are a conversation summarizer. Summarize the following chat messages and extract key points and action items.

Total messages: %d
%s

Return ONLY a JSON object in this exact format:
{
  "key_points": ["要点一", "要点二"],
  "action_items": ["待办事项一", "待办事项二"]
}

Rules:
- Respond in Chinese.
- key_points: summarize the main discussion points concisely. Each item is one sentence.
- action_items: extract ONLY actionable, concrete todo items from the conversation. If there are none, return an empty array [].
- Do NOT include action items in key_points. The two arrays must be disjoint.`

type llmResponse struct {
	KeyPoints   []string `json:"key_points"`
	ActionItems []string `json:"action_items"`
}

type SummarizeResult struct {
	Summary       string
	Todos         []string
	TotalMessages int
}

func Summarize(ctx context.Context, input *Input, messageText string, totalCount int) (*SummarizeResult, error) {
	prompt := fmt.Sprintf(summarizePrompt, totalCount, messageText)
	messages := []*schema.Message{
		{Role: schema.System, Content: prompt},
	}
	result, err := input.ChatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("summarize call failed: %w", err)
	}

	// Parse JSON response — the model may wrap it in ```json fences
	raw := result.Content
	if i := strings.Index(raw, "```"); i >= 0 {
		// Extract content between optional ```json ... ``` fences
		start := strings.Index(raw[i:], "\n")
		if start < 0 {
			start = 0
		} else {
			start = i + start + 1
		}
		end := strings.LastIndex(raw, "```")
		if end > start {
			raw = raw[start:end]
		}
	}
	raw = strings.TrimSpace(raw)

	var parsed llmResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		// Fallback: treat the whole response as summary, no todos
		logx.DefaultLogger().WithContext(ctx).Errorf("summarize json parse failed, fallback to raw: conv=%d err=%v", input.ConvID, err)
		return &SummarizeResult{
			Summary:       result.Content,
			Todos:         nil,
			TotalMessages: totalCount,
		}, nil
	}

	// Format key points as markdown for storage
	summary := ""
	if len(parsed.KeyPoints) > 0 {
		var b strings.Builder
		b.WriteString("## Key Points\n")
		for i, p := range parsed.KeyPoints {
			fmt.Fprintf(&b, "%d. %s\n", i+1, p)
		}
		summary = b.String()
	}

	logx.DefaultLogger().WithContext(ctx).Infof("summarize done: conv=%d messages=%d key_points=%d todos=%d",
		input.ConvID, totalCount, len(parsed.KeyPoints), len(parsed.ActionItems))

	return &SummarizeResult{
		Summary:       summary,
		Todos:         parsed.ActionItems,
		TotalMessages: totalCount,
	}, nil
}

func BuildMessageText(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[user_%d]: %s\n", m.SenderID, m.Content)
	}
	return b.String()
}
