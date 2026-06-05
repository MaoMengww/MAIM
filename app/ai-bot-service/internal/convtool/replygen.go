package convtool

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const replyCandidatesPrompt = `Based on the conversation context below, generate 3 short reply suggestions (each no more than 15 characters). Output one per line without numbering or extra content:

Context:
%s`

func GenerateReplyCandidates(ctx context.Context, input *Input, contextText string) ([]string, error) {
	prompt := fmt.Sprintf(replyCandidatesPrompt, contextText)
	messages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}
	result, err := input.ChatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("reply candidates call failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(result.Content), "\n")
	candidates := make([]string, 0, 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "0123456789.、 ")
		if line != "" && len([]rune(line)) <= 20 {
			candidates = append(candidates, line)
		}
	}
	if len(candidates) > 3 {
		candidates = candidates[:3]
	}
	return candidates, nil
}
