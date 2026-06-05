package convtool

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/maomeng/aim/pkg/logx"
)

const summarizePrompt = `You are a conversation summarizer. Summarize the following chat messages and extract key points and action items.

Total messages: %d
%s

Return in the following format:
## Key Points
1. ...
2. ...

## Action Items
- [ ] description
- [ ] description`

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

	text := result.Content
	var todos []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ] ") || strings.HasPrefix(trimmed, "- [x] ") {
			todos = append(todos, strings.TrimPrefix(strings.TrimPrefix(trimmed, "- [x] "), "- [ ] "))
		}
	}

	logx.DefaultLogger().WithContext(ctx).Infof("summarize done: conv=%d messages=%d summary_len=%d todos=%d",
		input.ConvID, totalCount, len(text), len(todos))

	return &SummarizeResult{
		Summary:       text,
		Todos:         todos,
		TotalMessages: totalCount,
	}, nil
}

func BuildMessageText(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(fmt.Sprintf("[user_%d]: %s\n", m.SenderID, m.Content))
	}
	return b.String()
}
