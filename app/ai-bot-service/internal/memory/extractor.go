package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
	return &Extractor{chatModel: chatModel, config: config}
}

// ExtractInput is the source message for memory extraction.
type ExtractInput struct {
	BotID            int64
	UserID           int64
	ConvID           int64
	MsgID            int64
	Username         string
	Message          string
	SentAt           time.Time
	OwnerID          int64
	EmbeddingModelID int64
	MemoryModelID    int64
	MemoryModelName  string
}

// ExtractResult holds the extraction output.
type ExtractResult struct {
	Facts []ExtractedFact `json:"facts"`
}

// ExtractedFact is a single structured fact.
type ExtractedFact struct {
	Subject      string  `json:"subject"`
	Predicate    string  `json:"predicate"`
	Object       string  `json:"object"`
	EntityType   string  `json:"entity_type"`
	Category     string  `json:"category"`
	Content      string  `json:"content"`
	Evidence     string  `json:"evidence"`
	Importance   float64 `json:"importance"`
	Confidence   float64 `json:"confidence"`
	TemporalHint string  `json:"temporal_hint"`
}

func (e *Extractor) Extract(ctx context.Context, input ExtractInput) (*ExtractResult, error) {
	prompt := buildExtractionPrompt(input)
	messages := []*schema.Message{{Role: schema.User, Content: prompt}}
	opts := []einoModel.Option{einoModel.WithTemperature(float32(e.config.Temperature))}

	result, err := e.chatModel.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, fmt.Errorf("memory extraction llm call failed: %w", err)
	}

	parsedResult, err := ParseExtractionResult(result.Content)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(parsedResult.Facts))
	filtered := make([]ExtractedFact, 0, len(parsedResult.Facts))
	for _, f := range parsedResult.Facts {
		f.Predicate = normalizePredicate(f.Predicate)
		f.Object = strings.TrimSpace(f.Object)
		f.EntityType = strings.TrimSpace(strings.ToLower(f.EntityType))
		f.Category = strings.TrimSpace(strings.ToLower(f.Category))
		f.TemporalHint = normalizeTemporalHint(f.TemporalHint)
		if f.Confidence < 0.6 || f.Importance < 0.3 || f.Content == "" || f.Predicate == "" || f.Object == "" {
			continue
		}
		if friendlyPhrases(f.Content) {
			continue
		}
		key := strings.Join([]string{f.Predicate, normalizeEntityName(f.Object), strings.ToLower(strings.TrimSpace(f.Content))}, "|")
		if seen[key] {
			continue
		}
		seen[key] = true
		filtered = append(filtered, f)
	}
	parsedResult.Facts = filtered
	return parsedResult, nil
}

func normalizeTemporalHint(hint string) string {
	switch strings.ToLower(strings.TrimSpace(hint)) {
	case "current", "past", "future":
		return strings.ToLower(strings.TrimSpace(hint))
	default:
		return "unknown"
	}
}

// friendlyPhrases detects pure chit-chat that should not be persisted.
func friendlyPhrases(content string) bool {
	lower := strings.ToLower(content)
	trivial := []string{"hello", "hi", "hey", "nice to meet", "how are you", "good morning", "good afternoon", "good evening", "thanks", "thank you", "you're welcome", "bye", "goodbye", "see you"}
	for _, p := range trivial {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
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

func buildExtractionPrompt(input ExtractInput) string {
	predicates := availablePredicates()
	categories := availableCategories()
	return fmt.Sprintf(`Extract long-term user memories from one user message.

Target user_id: %d
Target username: %s
Message time: %s

Categories: %s
Predicates: %s
Temporal hints: current | past | future | unknown

Rules:
- Only extract facts explicitly stated by the target user.
- Do not infer facts from assistant replies or third-party mentions.
- Return an empty array for greetings, thanks, casual chit-chat, one-off requests, or questions without durable user facts.
- Each fact must be a Subject-Predicate-Object triple.
- Predicate must be one from the Predicates list above. Choose the most specific one that fits.
- Use temporal_hint=current for now/recently/currently facts.
- Use temporal_hint=past for before/previously/used to/last year/high school facts.
- Use temporal_hint=future for plans or intentions.
- Use temporal_hint=unknown if no time signal is present.
- importance 0.0~1.0, 0.3=normal, 0.5=important, 0.8=critical.
- confidence 0.0~1.0.

User message:
%s

Output JSON only:
{"facts":[{"subject":"user","predicate":"main_language","object":"Go","entity_type":"language","category":"skill","content":"用户现在主要使用 Go","evidence":"我现在主要写 Go","importance":0.7,"confidence":0.9,"temporal_hint":"current"}]}`,
		input.UserID, input.Username, input.SentAt.Format(time.RFC3339),
		strings.Join(categories, " | "), strings.Join(predicates, ", "),
		input.Message)
}

// availablePredicates returns all predicate names registered in the predicateRegistry.
func availablePredicates() []string {
	predicates := make([]string, 0, len(predicateRegistry))
	for p := range predicateRegistry {
		predicates = append(predicates, p)
	}
	sort.Strings(predicates)
	return predicates
}

// availableCategories returns unique, sorted category names from the predicateRegistry.
func availableCategories() []string {
	seen := make(map[string]bool, len(predicateRegistry))
	for _, cfg := range predicateRegistry {
		if cfg.Category != "" && !seen[cfg.Category] {
			seen[cfg.Category] = true
		}
	}
	categories := make([]string, 0, len(seen))
	for c := range seen {
		categories = append(categories, c)
	}
	sort.Strings(categories)
	return categories
}
