package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type RerankItem struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
	Document       string  `json:"document,omitempty"`
}

type RerankResult struct {
	Items  []RerankItem `json:"items"`
	Tokens int          `json:"tokens"`
}

// Remote semantic reranking stays on the native Cohere/DashScope APIs. Eino's
// document score sorter does not replace inference by these providers.
func Rerank(ctx context.Context, provider, apiKey, baseURL, model, query string, docs []string, topN int, returnDocs bool) (*RerankResult, error) {
	switch provider {
	case "cohere":
		return cohereRerank(ctx, apiKey, baseURL, model, query, docs, topN, returnDocs)
	default:
		return dashscopeRerank(ctx, apiKey, baseURL, model, query, docs, topN, returnDocs)
	}
}

func dashscopeRerank(ctx context.Context, apiKey, baseURL, model, query string, docs []string, topN int, returnDocs bool) (*RerankResult, error) {
	url := strings.TrimRight(baseURL, "/") + "/reranks"
	body := map[string]any{"model": model, "query": query, "documents": docs, "top_n": topN}
	respBytes, err := DoRequest(ctx, url, apiKey, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Output struct {
			Results []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			} `json:"results"`
		} `json:"output"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("rerank response invalid")
	}
	out := &RerankResult{Tokens: result.Usage.TotalTokens}
	for _, r := range result.Output.Results {
		if r.Index < 0 || r.Index >= len(docs) {
			return nil, fmt.Errorf("rerank response index invalid")
		}
		doc := ""
		if returnDocs {
			doc = docs[r.Index]
		}
		out.Items = append(out.Items, RerankItem{Index: r.Index, RelevanceScore: r.RelevanceScore, Document: doc})
	}
	return out, nil
}

func cohereRerank(ctx context.Context, apiKey, baseURL, model, query string, docs []string, topN int, returnDocs bool) (*RerankResult, error) {
	body := map[string]any{
		"model":     model,
		"query":     query,
		"documents": docs,
		"top_n":     topN,
	}
	respBytes, err := DoRequest(ctx, strings.TrimRight(baseURL, "/")+"/v2/rerank", apiKey, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
		Meta struct {
			BilledUnits struct {
				SearchUnits int `json:"search_units"`
			} `json:"billed_units"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("cohere rerank response invalid")
	}
	out := &RerankResult{Tokens: result.Meta.BilledUnits.SearchUnits}
	for _, r := range result.Results {
		if r.Index < 0 || r.Index >= len(docs) {
			return nil, fmt.Errorf("rerank response index invalid")
		}
		doc := ""
		if returnDocs {
			doc = docs[r.Index]
		}
		out.Items = append(out.Items, RerankItem{Index: r.Index, RelevanceScore: r.RelevanceScore, Document: doc})
	}
	return out, nil
}
