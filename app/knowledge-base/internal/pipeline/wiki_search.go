package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/logx"
)

type WikiSearchPipeline struct {
	WikiRepo   domain.WikiPageRepo
	LLMGateway LLMGateway
	Logger     logx.Logger
}

type WikiSearchResult struct {
	Slug     string  `json:"slug"`
	Title    string  `json:"title"`
	Content  string  `json:"content"`
	Score    float32 `json:"score"`
	PageType string  `json:"page_type"`
}

func NewWikiSearchPipeline(wikiRepo domain.WikiPageRepo, llmGW LLMGateway, logger logx.Logger) *WikiSearchPipeline {
	return &WikiSearchPipeline{WikiRepo: wikiRepo, LLMGateway: llmGW, Logger: logger}
}

func (p *WikiSearchPipeline) SearchByIndex(ctx context.Context, kbID int64, query string, modelID int64, ownerID int64, topK int) ([]WikiSearchResult, error) {
	indexPage, err := p.WikiRepo.GetBySlug(ctx, kbID, "index")
	if err != nil {
		logger := p.Logger.WithContext(ctx)
		logger.Infof("wiki index not found for kb %d", kbID)
		return nil, nil
	}

	prompt := fmt.Sprintf(`You are a knowledge base navigation assistant. Find the most relevant pages from the index based on the user's question.

## Index Content
%s

## User Question
%s

## Output Requirements
Output only a JSON array, e.g. ["slug1", "slug2"], max 5. Output [] if not relevant. No other text.`, indexPage.Content, query)

	resp, err := p.LLMGateway.Chat(ctx, &LLMChatRequest{
		ModelId: modelID,
		OwnerID: ownerID,
		Messages: []*LLMMessage{
			{Role: "system", Content: "Output only a JSON array, no other text."},
			{Role: "user", Content: prompt},
		},
		Params: map[string]any{"temperature": 0.1},
	})
	if err != nil {
		return nil, fmt.Errorf("wiki llm match failed: %w", err)
	}

	var slugs []string
	if err := json.Unmarshal([]byte(resp.Content), &slugs); err != nil {
		return nil, nil
	}
	if len(slugs) == 0 {
		return nil, nil
	}
	if len(slugs) > topK {
		slugs = slugs[:topK]
	}

	pages, err := p.WikiRepo.GetBySlugs(ctx, kbID, slugs)
	if err != nil {
		return nil, err
	}

	results := make([]WikiSearchResult, 0, len(pages))
	for _, page := range pages {
		results = append(results, WikiSearchResult{
			Slug:     page.Slug,
			Title:    page.Title,
			Content:  page.Content,
			Score:    1.0,
			PageType: string(page.PageType),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}
