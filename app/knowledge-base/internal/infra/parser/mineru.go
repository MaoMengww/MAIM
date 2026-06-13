package parser

import (
	"archive/zip"
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
	if isBinary(raw) {
		return nil, domain.ErrParserUnsupported
	}
	return p.parseFile(ctx, raw)
}

// parseFile implements the cloud MinerU Precision API batch file upload flow:
//  1. POST /api/v4/file-urls/batch → batch_id + signed upload URL
//  2. PUT file bytes to the signed URL (24h expiry, no Content-Type needed)
//  3. Poll GET /api/v4/extract-results/batch/{batch_id} until done
//  4. Download the result zip and extract full.md + images
func (p *MinerUCloudParser) parseFile(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	ext := detectExtension(raw)
	fileName := "document." + ext

	// Step 1: request a signed upload URL
	batchID, uploadURL, err := p.requestBatchUpload(ctx, fileName)
	if err != nil {
		return nil, fmt.Errorf("mineru batch upload request: %w", err)
	}

	// Step 2: PUT file to the signed URL
	if err := p.uploadToSignedURL(ctx, uploadURL, raw); err != nil {
		return nil, fmt.Errorf("mineru file upload: %w", err)
	}

	// Step 3: poll for batch result
	fullZipURL, err := p.pollBatchResult(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("mineru batch poll: %w", err)
	}

	// Step 4: download zip and parse
	return p.downloadAndParseZip(ctx, fullZipURL)
}

// requestBatchUpload calls POST /api/v4/file-urls/batch and returns the
// batch_id and the first file's signed upload URL.
func (p *MinerUCloudParser) requestBatchUpload(ctx context.Context, fileName string) (batchID, uploadURL string, err error) {
	enableFormula := true
	enableTable := true
	reqBody := mineruBatchFileURLRequest{
		Files: []mineruBatchFile{
			{Name: fileName},
		},
		ModelVersion:  "vlm",
		EnableFormula: &enableFormula,
		EnableTable:   &enableTable,
		Language:      "ch",
	}

	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/file-urls/batch", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("batch request failed: %w", err)
	}
	defer resp.Body.Close()

	var result mineruBatchFileURLResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode batch response: %w", err)
	}
	if result.Code != 0 {
		return "", "", fmt.Errorf("mineru batch error code=%d: %s", result.Code, result.Msg)
	}
	if len(result.Data.FileURLs) == 0 {
		return "", "", fmt.Errorf("mineru returned empty file_urls")
	}

	return result.Data.BatchID, result.Data.FileURLs[0], nil
}

// uploadToSignedURL PUTs the file content to the pre-signed OSS URL.
func (p *MinerUCloudParser) uploadToSignedURL(ctx context.Context, url string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	// Per API docs: no Content-Type header needed — OSS auto-detects

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// pollBatchResult polls GET /api/v4/extract-results/batch/{batchID} until
// the file reaches a terminal state. Returns the full_zip_url on success.
func (p *MinerUCloudParser) pollBatchResult(ctx context.Context, batchID string) (string, error) {
	pollURL := p.baseURL + "/extract-results/batch/" + batchID

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("poll request failed: %w", err)
		}

		var result mineruBatchResultResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if decodeErr != nil {
			return "", fmt.Errorf("decode poll response: %w", decodeErr)
		}
		if result.Code != 0 {
			return "", fmt.Errorf("mineru poll error code=%d: %s", result.Code, result.Msg)
		}
		if len(result.Data.ExtractResult) == 0 {
			return "", fmt.Errorf("mineru poll returned empty extract_result")
		}

		er := result.Data.ExtractResult[0]
		switch er.State {
		case "done":
			if er.FullZipURL == "" {
				return "", fmt.Errorf("mineru done but full_zip_url is empty")
			}
			return er.FullZipURL, nil
		case "failed":
			errMsg := er.ErrMsg
			if errMsg == "" {
				errMsg = "mineru cloud parsing failed"
			}
			return "", fmt.Errorf("mineru parsing failed: %s", errMsg)
		case "waiting-file", "pending", "running", "converting":
			time.Sleep(2 * time.Second)
		default:
			return "", fmt.Errorf("mineru unknown state: %s", er.State)
		}
	}
}

// downloadAndParseZip downloads the result zip from full_zip_url and extracts
// the markdown content and images.
func (p *MinerUCloudParser) downloadAndParseZip(ctx context.Context, zipURL string) (*domain.ParsedDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zipURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download zip failed: %w", err)
	}
	defer resp.Body.Close()

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read zip body: %w", err)
	}

	return extractMarkdownFromZip(zipBytes)
}

// ---- Zip extraction ----

// extractMarkdownFromZip reads a zip archive from memory and returns a
// ParsedDocument with the full.md content and embedded images.
func extractMarkdownFromZip(data []byte) (*domain.ParsedDocument, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// Not a valid zip — try parseResult as fallback (JSON or raw text)
		return parseResult(data)
	}

	var mdContent string
	var images []domain.ImageRef
	imgIdx := 0

	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}

		// Extract full.md as the main markdown
		if f.Name == "full.md" || strings.HasSuffix(f.Name, "/full.md") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			mdContent = string(content)
			continue
		}

		// Extract images from the images/ directory
		if strings.HasPrefix(f.Name, "images/") {
			ext := strings.ToLower(filepath.Ext(f.Name))
			contentType := mimeByExt(ext)
			if contentType == "" {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			imgData, err := io.ReadAll(rc)
			rc.Close()
			if err != nil || len(imgData) == 0 {
				continue
			}
			if isIconImage(imgData) {
				continue
			}
			images = append(images, domain.ImageRef{
				Index:       imgIdx,
				RawContent:  imgData,
				ContentType: contentType,
				AltText:     filepath.Base(f.Name),
			})
			imgIdx++
		}
	}

	if mdContent == "" {
		return nil, fmt.Errorf("mineru zip contains no full.md")
	}

	return &domain.ParsedDocument{
		RawText:  mdContent,
		Images:   images,
		Metadata: domain.DocumentMeta{},
	}, nil
}

// mimeByExt maps common image file extensions to MIME types.
func mimeByExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".webp":
		return "image/webp"
	case ".tiff", ".tif":
		return "image/tiff"
	default:
		return ""
	}
}

// ---- File type detection ----

// detectExtension examines the file header bytes to guess a file extension.
// This is needed because MinerU's batch upload API requires a filename with
// the correct extension to determine how to process the file.
func detectExtension(raw []byte) string {
	if len(raw) < 4 {
		return "bin"
	}

	// PDF: %PDF
	if string(raw[:4]) == "%PDF" {
		return "pdf"
	}

	// PNG: \x89PNG
	if len(raw) >= 8 && raw[0] == 0x89 && raw[1] == 0x50 && raw[2] == 0x4E && raw[3] == 0x47 {
		return "png"
	}

	// JPEG: \xFF\xD8\xFF
	if raw[0] == 0xFF && raw[1] == 0xD8 && raw[2] == 0xFF {
		return "jpg"
	}

	// GIF: GIF8
	if len(raw) >= 6 && (string(raw[:4]) == "GIF8") {
		return "gif"
	}

	// BMP: BM
	if raw[0] == 'B' && raw[1] == 'M' {
		return "bmp"
	}

	// WebP: RIFF....WEBP
	if len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP" {
		return "webp"
	}

	// ZIP-based formats (docx, pptx, xlsx): PK\x03\x04
	if raw[0] == 0x50 && raw[1] == 0x4B && len(raw) >= 4 && raw[2] == 0x03 && raw[3] == 0x04 {
		// Default to docx for ZIP-based office files
		return "docx"
	}

	// TIFF: II*\x00 or MM\x00*
	if (raw[0] == 0x49 && raw[1] == 0x49 && raw[2] == 0x2A && raw[3] == 0x00) ||
		(raw[0] == 0x4D && raw[1] == 0x4D && raw[2] == 0x00 && raw[3] == 0x2A) {
		return "tiff"
	}

	// Default: treat as PDF (most common document format)
	return "pdf"
}

// ---- Result Parsing (shared) ----

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

	// Fallback: treat as raw text
	return &domain.ParsedDocument{
		RawText:  string(body),
		Metadata: domain.DocumentMeta{},
	}, nil
}

func decodeImage(b64 string) ([]byte, string) {
	// data:image/png;base64,...
	if prefix, b64data, found := strings.Cut(b64, ","); found {
		// Extract mime type from data URI
		if mimePart, _, ok := strings.Cut(prefix, ";"); ok {
			if _, ctype, ok := strings.Cut(mimePart, ":"); ok {
				if data, err := base64.StdEncoding.DecodeString(b64data); err == nil {
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

// -- Self-hosted API types --

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

// -- Cloud batch API types --

// mineruBatchFileURLRequest is the request body for POST /api/v4/file-urls/batch.
type mineruBatchFileURLRequest struct {
	Files         []mineruBatchFile `json:"files"`
	ModelVersion  string            `json:"model_version,omitempty"`
	EnableFormula *bool             `json:"enable_formula,omitempty"`
	EnableTable   *bool             `json:"enable_table,omitempty"`
	Language      string            `json:"language,omitempty"`
}

type mineruBatchFile struct {
	Name       string `json:"name"`
	DataID     string `json:"data_id,omitempty"`
	IsOCR      *bool  `json:"is_ocr,omitempty"`
	PageRanges string `json:"page_ranges,omitempty"`
}

// mineruBatchFileURLResponse is the response from POST /api/v4/file-urls/batch.
type mineruBatchFileURLResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		BatchID  string   `json:"batch_id"`
		FileURLs []string `json:"file_urls"`
	} `json:"data"`
}

// mineruBatchResultResponse is the response from GET /api/v4/extract-results/batch/{batch_id}.
type mineruBatchResultResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		BatchID       string                    `json:"batch_id"`
		ExtractResult []mineruBatchExtractResult `json:"extract_result"`
	} `json:"data"`
}

type mineruBatchExtractResult struct {
	FileName        string                  `json:"file_name"`
	State           string                  `json:"state"`
	FullZipURL      string                  `json:"full_zip_url"`
	ErrMsg          string                  `json:"err_msg"`
	DataID          string                  `json:"data_id"`
	ExtractProgress *mineruExtractProgress  `json:"extract_progress,omitempty"`
}

type mineruExtractProgress struct {
	ExtractedPages int    `json:"extracted_pages"`
	TotalPages     int    `json:"total_pages"`
	StartTime      string `json:"start_time"`
}
