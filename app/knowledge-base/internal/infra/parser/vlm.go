package parser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

type VLMTranscriber struct {
	provider string
	model    string
	apiKey   string
	baseURL  string
	client   *http.Client
}

func NewVLMTranscriber(cfg domain.VLMConfig) *VLMTranscriber {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	return &VLMTranscriber{
		provider: cfg.Provider,
		model:    cfg.Model,
		apiKey:   cfg.APIKey,
		baseURL:  cfg.BaseURL,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (v *VLMTranscriber) Describe(ctx context.Context, images []domain.ImageRef) string {
	if len(images) == 0 || v.apiKey == "" {
		return ""
	}

	var descriptions []string
	for _, img := range images {
		if len(img.RawContent) == 0 {
			continue
		}
		b64 := base64.StdEncoding.EncodeToString(img.RawContent)
		mediaType := img.ContentType
		if mediaType == "" {
			mediaType = "image/png"
		}
		dataURL := fmt.Sprintf("data:%s;base64,%s", mediaType, b64)

		desc, err := v.describeImage(ctx, dataURL)
		if err != nil {
			continue
		}
		descriptions = append(descriptions, desc)
	}

	if len(descriptions) == 0 {
		return ""
	}
	return strings.Join(descriptions, "\n\n")
}

func (v *VLMTranscriber) describeImage(ctx context.Context, dataURL string) (string, error) {
	payload := map[string]any{
		"model": v.model,
		"messages": []map[string]any{
			{
				"role": "system",
				"content": "You are a document image analyzer. Describe the content of the image in detail in Chinese, " +
					"focusing on text, data, relationships, and structure visible in the image.",
			},
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "请详细描述这张图片的内容，包括其中的文字、数据、表格、图表等信息。"},
					{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
				},
			},
		},
		"max_tokens": 1024,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal vlm request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create vlm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vlm request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read vlm response: %w", err)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode vlm response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("vlm returned no choices")
	}

	return fmt.Sprintf("<!-- VLM: %s -->", result.Choices[0].Message.Content), nil
}
