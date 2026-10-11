package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/maomeng/aim/pkg/logx"
)

func CreateEmbeddings(ctx context.Context, provider, apiKey, baseURL, model string, inputs []string, dimensions ...int) ([][]float32, *int, error) {
	switch provider {
	case "cohere":
		return cohereEmbed(ctx, apiKey, baseURL, model, inputs, dimensions...)
	default:
		return openaiCompatEmbed(ctx, apiKey, baseURL, model, inputs, dimensions...)
	}
}

func openaiCompatEmbed(ctx context.Context, apiKey, baseURL, model string, inputs []string, dimensions ...int) ([][]float32, *int, error) {
	if len(inputs) == 0 {
		return nil, nil, &RequestError{Err: fmt.Errorf("embedding inputs required")}
	}
	baseURL, err := ChatBaseURL("openai", baseURL)
	if err != nil {
		return nil, nil, err
	}
	bridge := &embeddingWireBridge{inputCount: len(inputs)}
	config := &openai.EmbeddingConfig{
		APIKey: apiKey, BaseURL: baseURL, Model: model,
		HTTPClient: &http.Client{Transport: bridge, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	if len(dimensions) > 0 && dimensions[0] > 0 {
		config.Dimensions = &dimensions[0]
	}
	component, err := openai.NewEmbedder(ctx, config)
	if err != nil {
		return nil, nil, fmt.Errorf("embedding component initialization failed")
	}
	vectors, err := component.EmbedStrings(ctx, inputs)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if bridge.err != nil {
			return nil, nil, bridge.err
		}
		return nil, nil, fmt.Errorf("embedding provider response invalid")
	}
	if len(vectors) != len(inputs) || len(bridge.indices) != len(inputs) {
		return nil, nil, fmt.Errorf("embedding response count mismatch")
	}
	dimension := len(vectors[0])
	if config.Dimensions != nil {
		dimension = *config.Dimensions
	}
	result := make([][]float32, len(inputs))
	for i, vector := range vectors {
		if dimension == 0 || len(vector) != dimension {
			return nil, nil, fmt.Errorf("embedding response dimension mismatch")
		}
		converted := make([]float32, len(vector))
		for j, value := range vector {
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
				return nil, nil, fmt.Errorf("embedding response contains invalid float")
			}
			converted[j] = float32(value)
		}
		result[bridge.indices[i]] = converted
	}
	return result, bridge.promptTokens, nil
}

// The locked component drops index and treats absent usage as zero. Observe only
// this metadata; Eino still owns request rendering and vector decoding.
type embeddingWireBridge struct {
	inputCount   int
	indices      []int
	promptTokens *int
	err          error
}

func (b *embeddingWireBridge) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		b.err = fmt.Errorf("embedding provider transport failed")
		return nil, b.err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		b.err = &HTTPError{StatusCode: response.StatusCode}
		return nil, b.err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	response.Body.Close()
	if err != nil {
		b.err = fmt.Errorf("embedding provider response read failed")
		return nil, b.err
	}
	if len(data) > maxProviderResponseBytes {
		b.err = fmt.Errorf("embedding provider response too large")
		return nil, b.err
	}
	var metadata struct {
		Data []struct {
			Index *int `json:"index"`
		} `json:"data"`
		Usage *struct {
			PromptTokens *int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		b.err = fmt.Errorf("embedding provider response invalid")
		return nil, b.err
	}
	if len(metadata.Data) != b.inputCount {
		b.err = fmt.Errorf("embedding response count mismatch")
		return nil, b.err
	}
	seen := make([]bool, b.inputCount)
	b.indices = make([]int, b.inputCount)
	for i, entry := range metadata.Data {
		if entry.Index == nil || *entry.Index < 0 || *entry.Index >= b.inputCount || seen[*entry.Index] {
			b.err = fmt.Errorf("embedding response index invalid")
			return nil, b.err
		}
		seen[*entry.Index] = true
		b.indices[i] = *entry.Index
	}
	if metadata.Usage != nil {
		b.promptTokens = metadata.Usage.PromptTokens
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

// Cohere keeps its native request/response contract; it is not an OpenAI embedder.
func cohereEmbed(ctx context.Context, apiKey, baseURL, model string, inputs []string, dimensions ...int) ([][]float32, *int, error) {
	if len(inputs) == 0 {
		return nil, nil, &RequestError{Err: fmt.Errorf("embedding inputs required")}
	}
	logger := logx.DefaultLogger().WithContext(ctx)
	body := map[string]any{"model": model, "texts": inputs, "input_type": "search_document", "embedding_types": []string{"float"}}
	respBytes, err := DoRequest(ctx, strings.TrimRight(baseURL, "/")+"/v2/embed", apiKey, body)
	if err != nil {
		logger.Errorf("model=%s cohere embed error: %v", model, err)
		return nil, nil, err
	}
	var result struct {
		Embeddings struct {
			Float [][]float32 `json:"float"`
		} `json:"embeddings"`
		Meta struct {
			BilledUnits struct {
				InputTokens *int `json:"input_tokens"`
			} `json:"billed_units"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		logger.Errorf("model=%s cohere embed response invalid", model)
		return nil, nil, fmt.Errorf("cohere embed response invalid")
	}
	if len(result.Embeddings.Float) != len(inputs) {
		return nil, nil, fmt.Errorf("embedding response count mismatch")
	}
	dimension := len(result.Embeddings.Float[0])
	if len(dimensions) > 0 && dimensions[0] > 0 {
		dimension = dimensions[0]
	}
	for _, vector := range result.Embeddings.Float {
		if dimension == 0 || len(vector) != dimension {
			return nil, nil, fmt.Errorf("embedding response dimension mismatch")
		}
	}
	if result.Meta.BilledUnits.InputTokens != nil {
		logger.Infof("model=%s input_tokens=%d embeddings=%d", model, *result.Meta.BilledUnits.InputTokens, len(result.Embeddings.Float))
	} else {
		logger.Infof("model=%s embeddings=%d", model, len(result.Embeddings.Float))
	}
	return result.Embeddings.Float, result.Meta.BilledUnits.InputTokens, nil
}
