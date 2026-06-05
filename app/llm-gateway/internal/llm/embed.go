package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maomeng/aim/pkg/logx"
)

func CreateEmbeddings(ctx context.Context, apiKey, baseURL, model string, inputs []string, dimensions ...int) ([][]float32, *int, error) {
	switch {
	case strings.Contains(baseURL, "cohere"):
		return cohereEmbed(ctx, apiKey, baseURL, model, inputs)
	default:
		return openaiCompatEmbed(ctx, apiKey, baseURL, model, inputs, dimensions...)
	}
}

func openaiCompatEmbed(ctx context.Context, apiKey, baseURL, model string, inputs []string, dimensions ...int) ([][]float32, *int, error) {
	logger := logx.DefaultLogger().WithContext(ctx)
	body := map[string]any{"model": model, "input": inputs}
	if len(dimensions) > 0 && dimensions[0] > 0 {
		body["dimensions"] = dimensions[0]
	}
	respBytes, err := DoRequest(ctx, strings.TrimRight(baseURL, "/")+"/embeddings", apiKey, body)
	if err != nil {
		logger.Errorf("model=%s embed error: %v", model, err)
		return nil, nil, err
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		logger.Errorf("model=%s embed decode error: %v", model, err)
		return nil, nil, fmt.Errorf("decode embed response: %w", err)
	}
	embeddings := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		embeddings[d.Index] = d.Embedding
	}
	logger.Infof("model=%s input_tokens=%d embeddings=%d", model, result.Usage.PromptTokens, len(embeddings))
	return embeddings, &result.Usage.PromptTokens, nil
}

func cohereEmbed(ctx context.Context, apiKey, baseURL, model string, inputs []string) ([][]float32, *int, error) {
	logger := logx.DefaultLogger().WithContext(ctx)
	body := map[string]any{"model": model, "texts": inputs, "input_type": "search_document"}
	respBytes, err := DoRequest(ctx, strings.TrimRight(baseURL, "/")+"/v2/embed", apiKey, body)
	if err != nil {
		logger.Errorf("model=%s cohere embed error: %v", model, err)
		return nil, nil, err
	}
	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
		Meta       struct {
			BilledUnits struct {
				InputTokens int `json:"input_tokens"`
			} `json:"billed_units"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		logger.Errorf("model=%s cohere embed decode error: %v", model, err)
		return nil, nil, fmt.Errorf("decode cohere embed response: %w", err)
	}
	logger.Infof("model=%s input_tokens=%d embeddings=%d", model, result.Meta.BilledUnits.InputTokens, len(result.Embeddings))
	return result.Embeddings, &result.Meta.BilledUnits.InputTokens, nil
}
