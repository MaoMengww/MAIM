package parser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

const (
	minImageDimension = 64
	minImageBytes     = 512
)

// ---- MinerU Self-hosted (mineru_precision) ----

type MinerUParser struct {
	apiURL   string
	apiToken string
	client   *http.Client
}

func NewMinerUParser(apiURL, apiToken string) *MinerUParser {
	return &MinerUParser{
		apiURL:   apiURL,
		apiToken: apiToken,
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *MinerUParser) Name() string { return "mineru_precision" }

func (p *MinerUParser) Parse(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	if p.apiURL == "" {
		return nil, domain.ErrParserUnsupported
	}
	return p.parsePrecision(ctx, raw)
}

func (p *MinerUParser) parsePrecision(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/api/v4/extract/task", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create mineru precision task: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if p.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mineru precision request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mineru precision returned %d: %s", resp.StatusCode, string(body))
	}

	var result mineruTaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode mineru precision response: %w", err)
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("mineru precision error: %s", result.Msg)
	}

	return p.pollResult(ctx, result.Data.TaskID)
}

func (p *MinerUParser) pollResult(ctx context.Context, taskID string) (*domain.ParsedDocument, error) {
	pollURL := p.apiURL + "/api/v4/extract/task/" + taskID

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create poll request: %w", err)
		}
		if p.apiToken != "" {
			req.Header.Set("Authorization", "Bearer "+p.apiToken)
		}

		resp, err := p.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("poll request failed: %w", err)
		}

		var queryResult mineruQueryResponse
		if err := json.NewDecoder(resp.Body).Decode(&queryResult); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode poll response: %w", err)
		}
		resp.Body.Close()

		if queryResult.Code != 0 {
			return nil, fmt.Errorf("mineru query error: %s", queryResult.Msg)
		}

		switch queryResult.Data.State {
		case "done":
			body, err := p.downloadResult(ctx, queryResult.Data.FullZipURL)
			if err != nil {
				return nil, err
			}
			return parseResult(body)
		case "failed":
			errMsg := queryResult.Data.ErrMsg
			if errMsg == "" {
				errMsg = "mineru parsing failed"
			}
			return nil, fmt.Errorf("mineru parsing failed: %s", errMsg)
		case "running", "pending", "converting":
			time.Sleep(2 * time.Second)
			continue
		default:
			return nil, fmt.Errorf("mineru unknown state: %s", queryResult.Data.State)
		}
	}
}

func (p *MinerUParser) downloadResult(ctx context.Context, zipURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zipURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download result failed: %w", err)
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// ---- MinerU Cloud (mineru.net) ----

type MinerUCloudParser struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func NewMinerUCloudParser(apiKey, baseURL string) *MinerUCloudParser {
	if baseURL == "" {
		baseURL = "https://mineru.net/api/v4"
	}
	return &MinerUCloudParser{
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *MinerUCloudParser) Name() string { return "mineru" }

func (p *MinerUCloudParser) Parse(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	if p.apiKey == "" {
		return nil, domain.ErrParserUnsupported
	}
	return p.parseFile(ctx, raw)
}

func (p *MinerUCloudParser) parseFile(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	// mineru.net cloud upload + poll flow
	// 1. POST /api/v4/file-urls/batch to get upload URL
	// 2. PUT file to upload URL
	// 3. Poll GET /api/v4/extract-results/batch/{batch_id} for result
	// 4. Parse result JSON for md_content + images
	// TODO: implement full flow
	return &domain.ParsedDocument{
		RawText:  string(raw),
		Metadata: domain.DocumentMeta{},
	}, nil
}

// ---- Result Parsing ----

// mineruParseResponse mirrors the MinerU API response with markdown and images.
type mineruParseResponse struct {
	Results struct {
		Document struct {
			MDContent string            `json:"md_content"`
			Images    map[string]string `json:"images"`
		} `json:"document"`
		Files struct {
			MDContent string            `json:"md_content"`
			Images    map[string]string `json:"images"`
		} `json:"files"`
	} `json:"results"`
}

func parseResult(body []byte) (*domain.ParsedDocument, error) {
	// Try JSON first (mineru cloud / newer API versions)
	var resp mineruParseResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		mdContent := resp.Results.Document.MDContent
		if mdContent == "" {
			mdContent = resp.Results.Files.MDContent
		}
		images := resp.Results.Document.Images
		if len(images) == 0 {
			images = resp.Results.Files.Images
		}

		doc := &domain.ParsedDocument{
			RawText:  mdContent,
			Metadata: domain.DocumentMeta{},
		}

		idx := 0
		for path, b64 := range images {
			data, ctype := decodeImage(b64)
			if data == nil {
				continue
			}
			if isIconImage(data) {
				// Replace icon image ref with empty alt text
				doc.RawText = strings.ReplaceAll(doc.RawText, fmt.Sprintf("](%s)", path), "]()")
				continue
			}
			doc.Images = append(doc.Images, domain.ImageRef{
				Index:       idx,
				RawContent:  data,
				ContentType: ctype,
				AltText:     filepath.Base(path),
				MinioKey:    "", // set by caller
			})
			idx++
		}
		return doc, nil
	}

	// Fallback: treat as raw text (older API returns zip with text)
	return &domain.ParsedDocument{
		RawText:  string(body),
		Metadata: domain.DocumentMeta{},
	}, nil
}

func decodeImage(b64 string) ([]byte, string) {
	// data:image/png;base64,...
	if idx := strings.Index(b64, ","); idx >= 0 {
		// Extract mime type from data URI
		if parts := strings.SplitN(b64[:idx], ";", 2); len(parts) > 0 {
			if mimeParts := strings.SplitN(parts[0], ":", 2); len(mimeParts) == 2 {
				ctype := mimeParts[1]
				data, err := base64.StdEncoding.DecodeString(b64[idx+1:])
				if err == nil {
					return data, ctype
				}
			}
		}
	}
	// Raw base64
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, ""
	}
	return data, http.DetectContentType(data)
}

func isIconImage(data []byte) bool {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return len(data) < minImageBytes
	}
	return cfg.Width < minImageDimension && cfg.Height < minImageDimension
}

// ---- JSON response types ----

type mineruTaskResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		TaskID string `json:"task_id"`
	} `json:"data"`
}

type mineruQueryResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		TaskID     string `json:"task_id"`
		State      string `json:"state"`
		FullZipURL string `json:"full_zip_url"`
		ErrMsg     string `json:"err_msg"`
	} `json:"data"`
}
