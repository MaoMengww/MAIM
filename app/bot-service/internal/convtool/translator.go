package convtool

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const translatePrompt = `Translate the following text into %s. Preserve the original format and tone. Output the translation directly without any additional explanation:

%s`

type TranslateResult struct {
	TranslatedText string
	DetectedLang   string
}

func Translate(ctx context.Context, input *Input, text string, targetLang string) (*TranslateResult, error) {
	langLabel := targetLang
	switch targetLang {
	case "zh-CN":
		langLabel = "Simplified Chinese"
	case "en-US":
		langLabel = "English"
	case "ja":
		langLabel = "Japanese"
	case "ko":
		langLabel = "Korean"
	}

	prompt := fmt.Sprintf(translatePrompt, langLabel, text)
	messages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}
	result, err := input.ChatModel.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("translate call failed: %w", err)
	}

	return &TranslateResult{
		TranslatedText: strings.TrimSpace(result.Content),
		DetectedLang:   targetLang,
	}, nil
}
