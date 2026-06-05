package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/chunker"
	"github.com/maomeng/aim/app/knowledge-base/internal/infra/parser"
	"github.com/maomeng/aim/app/knowledge-base/internal/metrics"
	llmgateway "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/pkg/errors"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
	"github.com/zeromicro/go-zero/zrpc"
)

var mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)`)

// downloadImage downloads an image from a URL with a 30s timeout.
func downloadImage(url string) ([]byte, string, error) {
	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" || strings.HasPrefix(ct, "text/") {
		ct = "image/png"
	}
	return body, ct, nil
}

type IngestPipeline struct {
	Parser           domain.Parser
	Chunker          domain.Chunker
	Embedder         domain.Embedder
	VectorStore      domain.VectorStore
	FileStore        domain.FileStore
	DocRepo          domain.DocumentRepo
	KBRepo           domain.KBRepo
	Snowflake        *snowflake.Node
	LLMGatewayClient zrpc.Client
	RetryLimit       int
	MaxFileSize      int64
	Logger           logx.Logger
	Progress         func(ctx context.Context, doc *domain.Document, evt event.RealtimeEvent)
}

func (p *IngestPipeline) emitProgress(ctx context.Context, doc *domain.Document, evt event.RealtimeEvent) {
	if p.Progress != nil {
		p.Progress(ctx, doc, evt)
	}
}

func (p *IngestPipeline) Run(ctx context.Context, doc *domain.Document, cfg domain.PipelineConfig, embeddingModelID int64, ownerID int64) (err error) {
	start := time.Now()
	logger := p.Logger.WithContext(ctx)
	defer func() {
		duration := time.Since(start).Seconds()
		kbIDStr := strconv.FormatInt(doc.KBID, 10)
		status := "success"
		if err != nil {
			status = "failed"
			if updateErr := p.DocRepo.UpdateStatus(ctx, doc.ID, domain.DocStatusFailed, err.Error()); updateErr != nil {
				logger.Errorf("failed to update doc %d status to failed: %v", doc.ID, updateErr)
			}
		}
		metrics.KbIngestDuration.Observe(duration, status)
		metrics.KbDocumentTotal.Add(1, kbIDStr, status)
		// Always update counts after processing attempt
		if countErr := p.KBRepo.UpdateCounts(ctx, doc.KBID); countErr != nil {
			logger.Errorf("failed to update kb counts for kb %d: %v", doc.KBID, countErr)
		}
	}()
	var parsedContent *domain.ParsedDocument

	// Stage 1: Parse
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeParsing, Level: event.EventLevelInfo, Title: "æ­£å¨è§£æ", Message: "ææ¡£æ­£å¨è§£æ"})
	if err := p.runStage(ctx, doc, domain.DocStatusParsing, func() error {
		raw, err := p.FileStore.Get(ctx, doc.MinioKey)
		if err != nil {
			return errors.Wrap(errors.CodeIOError, "failed to get file from storage", err)
		}
		defer raw.Close()

		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(raw); err != nil {
			return errors.Wrap(errors.CodeIOError, "failed to read file", err)
		}
		fileBytes := buf.Bytes()

		if p.MaxFileSize > 0 && int64(len(fileBytes)) > p.MaxFileSize {
			return domain.ErrFileTooLarge
		}

		setupParser := p.Parser
		if setupParser == nil {
			setupParser = parser.SetupParser(cfg.Parsing, doc.FileType)
		}
		parsed, err := setupParser.Parse(ctx, fileBytes)
		if err != nil {
			return err
		}
		parsedContent = parsed

		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		doc.Metadata["parsed_title"] = parsed.Metadata.Title
		doc.Metadata["parsed_language"] = parsed.Metadata.Language
		doc.Metadata["page_count"] = parsed.Metadata.PageCount
		return nil
	}); err != nil {
		return err
	}

	// Resolve Markdown image links and download remote images
	resolveAndDownloadImages(ctx, parsedContent, p.Logger)

	// Stage 1.5: Process images (upload to MinIO + VLM transcription)
	if len(parsedContent.Images) > 0 {
		p.emitProgress(ctx, doc, event.RealtimeEvent{
			Type:    event.EventTypeKnowledgeParsing,
			Level:   event.EventLevelInfo,
			Title:   "处理图片",
			Message: fmt.Sprintf("å¤ç %d å¼ å¾ç", len(parsedContent.Images)),
		})
		for i := range parsedContent.Images {
			img := &parsedContent.Images[i]
			key := fmt.Sprintf("knowledge/%d/%d/images/%d", doc.KBID, doc.ID, img.Index)
			if err := p.FileStore.Put(ctx, key, bytes.NewReader(img.RawContent), int64(len(img.RawContent)), img.ContentType); err != nil {
				logger.Errorf("upload image %d failed: %v", img.Index, err)
				continue
			}
			img.MinioKey = key
		}
		// VLM transcription via llm-gateway
		if cfg.Parsing.VLM != nil && cfg.Parsing.VLM.Enabled && p.LLMGatewayClient != nil {
			conn := p.LLMGatewayClient.Conn()
			if conn != nil {
				cli := llmgateway.NewLLMGatewayClient(conn)
				var wg sync.WaitGroup
				var mu sync.Mutex
				for _, img := range parsedContent.Images {
					if len(img.RawContent) == 0 || img.URL == "" {
						continue
					}
					wg.Add(1)
					go func(img domain.ImageRef) {
						defer wg.Done()
						mime := img.ContentType
						if mime == "" {
							mime = "image/png"
						}
						dataURL := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(img.RawContent))
						resp, err := cli.VlmChat(ctx, &llmgateway.VlmChatReq{
							ModelId:    cfg.Parsing.VLM.ModelID,
							OwnerId:    ownerID,
							UserPrompt: "Please describe the content of this image in detail, including any text, data, tables, charts, and other information.",
							ImageData:  dataURL,
							MaxTokens:  1024,
						})
						if err != nil {
							p.Logger.WithContext(ctx).Errorf("vlm describe failed: %v", err)
							return
						}
						if len(resp.Choices) > 0 {
							orig := fmt.Sprintf("![%s](%s)", img.AltText, img.URL)
							figure := fmt.Sprintf("<figure>\n<img src=\"%s\" alt=\"%s\">\n<figcaption>%s</figcaption>\n</figure>",
								img.URL, img.AltText, resp.Choices[0].Message.Content)
							mu.Lock()
							parsedContent.RawText = strings.ReplaceAll(parsedContent.RawText, orig, figure)
							mu.Unlock()
						}
					}(img)
				}
				wg.Wait()
			}
		}
	}

	// Stage 2: Chunk
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeChunking, Level: event.EventLevelInfo, Title: "æ­£å¨åå", Message: "ææ¡£æ­£å¨ååä¸ºç¥è¯çæ®µ"})
	var pcResult *domain.ParentChildChunks
	if err := p.runStage(ctx, doc, domain.DocStatusChunking, func() error {
		setupChunker := p.Chunker
		if setupChunker == nil {
			setupChunker = chunker.NewChunker()
		}
		var err error
		pcResult, err = setupChunker.Chunk(ctx, parsedContent, cfg.Chunking)
		if err != nil {
			return err
		}

		for i := range pcResult.Parents {
			pcResult.Parents[i].KBID = doc.KBID
			pcResult.Parents[i].DocID = doc.ID
		}
		for i := range pcResult.Children {
			pcResult.Children[i].KBID = doc.KBID
			pcResult.Children[i].DocID = doc.ID
		}

		if err := p.DocRepo.DeleteChunksByDoc(ctx, doc.ID); err != nil {
			return errors.Wrap(errors.CodeDBError, "failed to delete old chunks", err)
		}

		// Store parent chunks and build index -> ID mapping
		parentIDMap := make(map[int]int64)
		for _, parent := range pcResult.Parents {
			parentRecord := &domain.ChunkRecord{
				ID:         p.Snowflake.Generate(),
				DocID:      doc.ID,
				KBID:       doc.KBID,
				ChunkIndex: parent.Index,
				Content:    CleanInvalidUTF8(parent.Content),
				TokenCount: parent.TokenCount,
				Metadata:   parent.Metadata,
			}
			if err := p.DocRepo.CreateParentChunk(ctx, parentRecord); err != nil {
				return errors.Wrap(errors.CodeDBError, "failed to create parent chunk", err)
			}
			parentIDMap[parent.Index] = parentRecord.ID
		}

		// Create child records with ParentChunkID
		records := make([]domain.ChunkRecord, len(pcResult.Children))
		for i, ch := range pcResult.Children {
			record := domain.ChunkRecord{
				ID:          p.Snowflake.Generate(),
				DocID:       ch.DocID,
				KBID:        ch.KBID,
				ChunkIndex:  ch.Index,
				Content:     CleanInvalidUTF8(ch.Content),
				TokenCount:  ch.TokenCount,
				MilvusDocID: ch.ID(),
				Metadata:    ch.Metadata,
			}
			if pi, ok := ch.Metadata["parent_index"].(int); ok {
				if parentID, exists := parentIDMap[pi]; exists {
					record.ParentChunkID = &parentID
				}
			}
			records[i] = record
		}
		if err := p.DocRepo.CreateChunks(ctx, records); err != nil {
			return errors.Wrap(errors.CodeDBError, "failed to create chunks", err)
		}

		doc.ChunkCount = len(records)
		if err := p.DocRepo.Update(ctx, doc); err != nil {
			return errors.Wrap(errors.CodeDBError, "failed to update chunk count", err)
		}
		return nil
	}); err != nil {
		return err
	}

	// Stage 3: Embed + Store
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeEmbedding, Level: event.EventLevelInfo, Title: "æ­£å¨åéå", Message: "ææ¡£æ­£å¨çæåé"})
	if err := p.runStage(ctx, doc, domain.DocStatusEmbedding, func() error {
		var vecDocs []domain.VectorDoc
		for _, ch := range pcResult.Children {
			if ch.Metadata == nil {
				ch.Metadata = make(map[string]any)
			}
			ch.Metadata["doc_title"] = doc.Title
			vd := domain.VectorDoc{
				DocID:    ch.ID(),
				KBID:     ch.KBID,
				Content:  CleanInvalidUTF8(ch.Content),
				Metadata: ch.Metadata,
			}
			vecDocs = append(vecDocs, vd)
		}

		childTexts := make([]string, len(vecDocs))
		childIndices := make([]int, len(vecDocs))
		for i, vd := range vecDocs {
			childTexts[i] = vd.Content
			childIndices[i] = i
		}

		if len(childTexts) > 0 && p.Embedder != nil {
			const batchSize = 10
			vectors := make([][]float32, len(childTexts))
			for start := 0; start < len(childTexts); start += batchSize {
				end := start + batchSize
				if end > len(childTexts) {
					end = len(childTexts)
				}
				batch, err := p.Embedder.Embed(ctx, childTexts[start:end], embeddingModelID, ownerID)
				if err != nil {
					return errors.Wrap(errors.CodeRPCError, "embedding failed", err)
				}
				copy(vectors[start:], batch)
			}
			for i, idx := range childIndices {
				if i < len(vectors) {
					vecDocs[idx].Vector = vectors[i]
				}
			}
		}

		if p.VectorStore != nil {
			if err := p.VectorStore.Insert(ctx, vecDocs); err != nil {
				return errors.Wrap(errors.CodeInternal, "vector store insert failed", err)
			}
		}

		return nil
	}); err != nil {
		return err
	}

	if err := p.DocRepo.UpdateStatus(ctx, doc.ID, domain.DocStatusReady, ""); err != nil {
		return err
	}
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeReady, Level: event.EventLevelSuccess, Title: "å¤çå®æ", Message: "ææ¡£å·²å®æå¤ç"})
	l := p.Logger.WithContext(ctx)
	l.Infof("ingest pipeline completed: doc_id=%d stages=3 duration=%s", doc.ID, time.Since(start))
	return nil
}

func (p *IngestPipeline) runStage(ctx context.Context, doc *domain.Document, status domain.DocStatus, fn func() error) error {
	l := p.Logger.WithContext(ctx)

	_ = p.DocRepo.UpdateStatus(ctx, doc.ID, status, "")

	limit := p.RetryLimit
	if limit <= 0 {
		limit = 3
	}

	var lastErr error
	for attempt := 0; attempt <= limit; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
			l.Infof("stage %s attempt %d/%d failed: %v", string(status), attempt+1, limit+1, err)
		}
		backoff := time.Duration(attempt+1) * time.Second
		if attempt > 0 {
			backoff = time.Duration(1<<uint(attempt)) * time.Second
		}
		time.Sleep(backoff)
	}

	return errors.Wrap(errors.CodeInternal, fmt.Sprintf("stage %s failed after %d tries", string(status), limit+1), lastErr)
}

// resolveAndDownloadImages scans parsedContent.RawText for Markdown image links
// ![alt](url), downloads each image concurrently (max 5 goroutines),
// and populates parsedContent.Images.
func resolveAndDownloadImages(ctx context.Context, doc *domain.ParsedDocument, logger logx.Logger) int {
	matches := mdImageRe.FindAllStringSubmatch(doc.RawText, -1)
	if len(matches) == 0 {
		return 0
	}

	type imgResult struct {
		AltText     string
		URL         string
		RawContent  []byte
		ContentType string
	}

	results := make(chan imgResult, len(matches))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for _, m := range matches {
		alt, url := m[1], m[2]
		wg.Add(1)
		go func(alt, url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			body, ct, err := downloadImage(url)
			if err != nil {
				logger.WithContext(ctx).Infof("download image %s failed: %v", url, err)
				return
			}
			results <- imgResult{
				AltText:     alt,
				URL:         url,
				RawContent:  body,
				ContentType: ct,
			}
		}(alt, url)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	downloaded := 0
	for r := range results {
		doc.Images = append(doc.Images, domain.ImageRef{
			Index:       downloaded,
			URL:         r.URL,
			RawContent:  r.RawContent,
			ContentType: r.ContentType,
			AltText:     r.AltText,
		})
		downloaded++
	}
	return downloaded
}

func CleanInvalidUTF8(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		if r == 0 {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}

	return b.String()
}
