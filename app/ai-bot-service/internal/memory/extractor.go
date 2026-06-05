package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ExtractorConfig configures the memory extractor.
type ExtractorConfig struct {
	ModelID     int64
	Temperature float64
	ModelName   string
}

// Extractor extracts structured memories from dialog.
type Extractor struct {
	chatModel einoModel.BaseChatModel
	config    ExtractorConfig
}

// NewExtractor creates a new Extractor.
func NewExtractor(chatModel einoModel.BaseChatModel, config ExtractorConfig) *Extractor {
	if config.Temperature <= 0 {
		config.Temperature = 0.3
	}
	return &Extractor{
		chatModel: chatModel,
		config:    config,
	}
}

// ExtractResult holds the extraction output.
type ExtractResult struct {
	Facts []ExtractedFact `json:"facts"`
}

// ExtractedFact is a single structured fact.
type ExtractedFact struct {
	Content    string  `json:"content"`
	Category   string  `json:"category"`
	Importance float64 `json:"importance"`
	Confidence float64 `json:"confidence"`
}

func (e *Extractor) Extract(ctx context.Context, dialog []Message) (*ExtractResult, error) {
	dialogText := formatDialog(dialog)
	prompt := fmt.Sprintf(memoryExtractionPrompt, dialogText)

	messages := []*schema.Message{
		{Role: schema.User, Content: prompt},
	}

	opts := []einoModel.Option{
		einoModel.WithTemperature(float32(e.config.Temperature)),
	}

	result, err := e.chatModel.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, fmt.Errorf("memory extraction llm call failed: %w", err)
	}

	parsedResult, err := ParseExtractionResult(result.Content)
	if err != nil {
		return nil, err
	}

	// Filter low-quality and duplicate facts
	seen := make(map[string]bool, len(parsedResult.Facts))
	filtered := make([]ExtractedFact, 0, len(parsedResult.Facts))
	for _, f := range parsedResult.Facts {
		if f.Confidence < 0.6 || f.Importance < 0.3 {
			continue
		}
		if friendlyPhrases(f.Content) {
			continue
		}
		// Dedup by content (normalized)
		key := strings.TrimSpace(strings.ToLower(f.Content))
		if seen[key] {
			continue
		}
		seen[key] = true
		filtered = append(filtered, f)
	}
	parsedResult.Facts = filtered

	return parsedResult, nil
}

// friendlyPhrases detects pure chit-chat that should not be persisted.
func friendlyPhrases(content string) bool {
	lower := strings.ToLower(content)
	trivial := []string{
		"hello", "hi", "hey", "nice to meet", "how are you",
		"good morning", "good afternoon", "good evening",
		"thanks", "thank you", "you're welcome",
		"bye", "goodbye", "see you",
	}
	for _, p := range trivial {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// Message is a dialog message.
type Message struct {
	Role    string
	Content string
}

func formatDialog(msgs []Message) string {
	var out string
	for _, m := range msgs {
		out += fmt.Sprintf("%s: %s\n", m.Role, m.Content)
	}
	return strings.TrimSpace(out)
}

func ParseExtractionResult(raw string) (*ExtractResult, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var result ExtractResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("parse extraction result: %w", err)
	}
	return &result, nil
}

const memoryExtractionPrompt = `Extract user memories from the following conversation. Return an empty array for casual chit-chat.

Categories: hobby | skill | occupation | experience | background | preference

Rules:
- Extract information worth remembering long-term (identity, experience, skills, preferences, etc.)
- Each fact should be one sentence, clear and concise
- Multiple facts allowed, but avoid duplicate or similar content
- importance 0.0~1.0, 0.3=normal 0.5=important 0.8=critical

Dialog:
%s

Output JSON:
{"facts":[{"content":"likes playing basketball","category":"hobby","importance":0.5,"confidence":0.9}]}`
