package pipeline

import (
	"bytes"
	"context"
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/zeromicro/go-zero/zrpc"
)

var mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)`)

// downloadImage downloads an image from a URL with a 30s timeout.
func downloadImage(ctx context.Context, url string) ([]byte, string, error) {
	cli := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := cli.Do(req)
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

func (p *IngestPipeline) Run(ctx context.Context, doc *domain.Document) error {
	return p.DocRepo.WithDocumentLock(ctx, doc.ID, func(ctx context.Context) error {
		current, err := p.DocRepo.Get(ctx, doc.ID)
		if err != nil {
			return err
		}
		if current.Status == domain.DocStatusReady || current.Status == domain.DocStatusFailed {
			return nil
		}
		// Holding the document lock excludes a live ingest attempt. A processing
		// state here is interrupted work from an unacknowledged queue delivery.
		kb, err := p.KBRepo.Get(ctx, current.KBID)
		if err != nil {
			return err
		}
		if kb.Status == "deleting" {
			return nil
		}
		*doc = *current
		return p.run(ctx, doc, kb)
	})
}

func (p *IngestPipeline) run(ctx context.Context, doc *domain.Document, kb *domain.KnowledgeBase) (err error) {
	start := time.Now()
	logger := p.Logger.WithContext(ctx)
	statusCtx, statusCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer statusCancel()
	defer func() {
		duration := time.Since(start).Seconds()
		status := "success"
		if err != nil {
			status = "failed"
			failureCtx, cancel := context.WithTimeout(statusCtx, 5*time.Second)
			defer cancel()
			if updateErr := p.DocRepo.UpdateStatus(failureCtx, doc.ID, domain.DocStatusFailed, err.Error()); updateErr != nil {
				logger.Errorf("failed to update doc %s status to failed: %v", doc.ID, updateErr)
				err = stderrors.Join(err, updateErr)
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(statusCtx, 30*time.Second)
			defer cleanupCancel()
			if cleanupErr := p.clearChunks(cleanupCtx, doc.ID); cleanupErr != nil {
				logger.Errorf("failed to clean chunks for doc %s: %v", doc.ID, cleanupErr)
				err = stderrors.Join(err, cleanupErr)
			}
			if cleanupErr := p.clearImages(cleanupCtx, doc); cleanupErr != nil {
				logger.Errorf("failed to clean images for doc %s: %v", doc.ID, cleanupErr)
				err = stderrors.Join(err, cleanupErr)
			}
			doc.Status, doc.ErrorMessage, doc.ChunkCount = domain.DocStatusFailed, err.Error(), 0
			updateCtx, updateCancel := context.WithTimeout(statusCtx, 5*time.Second)
			defer updateCancel()
			if updateErr := p.DocRepo.Update(updateCtx, doc); updateErr != nil {
				err = stderrors.Join(err, updateErr)
			}
			p.emitProgress(failureCtx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeFailed, Level: event.EventLevelError, Title: "入库失败", Message: err.Error()})
		}
		metrics.KbIngestDuration.Observe(duration, status)
		metrics.KbIngestTotal.Inc(status)
		metrics.KbDocumentTotal.Add(1, doc.KBID, status)
		// Always update counts after processing attempt
		countCtx, cancel := context.WithTimeout(statusCtx, 5*time.Second)
		defer cancel()
		if countErr := p.KBRepo.UpdateCounts(countCtx, doc.KBID); countErr != nil {
			logger.Errorf("failed to update kb counts for kb %s: %v", doc.KBID, countErr)
		}
	}()
	if kb.Status != "active" {
		return domain.ErrKBNotFound
	}
	cfg := kb.PipelineConfig
	if doc.PipelineOverride != nil {
		cfg = *doc.PipelineOverride
	}
	embeddingModelID, err := kb.ResolveEmbeddingModelID(ctx, p.KBRepo)
	if err != nil {
		return errors.Wrap(errors.CodeInvalidParam, "embedding model unavailable", err)
	}
	ownerID := kb.OwnerID
	doc.Stages = nil
	var parsedContent *domain.ParsedDocument
	if err := cfg.ValidateModelReferences(); err != nil {
		return errors.Wrap(errors.CodeInvalidParam, "invalid pipeline model reference", err)
	}
	// Pending documents are already hidden from retrieval. Remove both old
	// representations before creating the replacement under the same lock.
	if err := p.clearChunks(ctx, doc.ID); err != nil {
		return err
	}
	if err := p.clearImages(ctx, doc); err != nil {
		return err
	}

	// Stage 1: Parse
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeParsing, Level: event.EventLevelInfo, Title: "正在解析", Message: "文档正在解析"})
	if err := p.runStage(ctx, doc, domain.DocStatusParsing, func() error {
		raw, err := p.FileStore.Get(ctx, doc.MinioKey)
		if err != nil {
			return errors.Wrap(errors.CodeIOError, "failed to get file from storage", err)
		}
		defer raw.Close()

		buf := new(bytes.Buffer)
		var reader io.Reader = raw
		if p.MaxFileSize > 0 {
			reader = io.LimitReader(raw, p.MaxFileSize+1)
		}
		if _, err := buf.ReadFrom(reader); err != nil {
			return errors.Wrap(errors.CodeIOError, "failed to read file", err)
		}
		fileBytes := buf.Bytes()

		if p.MaxFileSize > 0 && int64(len(fileBytes)) > p.MaxFileSize {
			return domain.ErrFileTooLarge
		}

		setupParser := p.Parser
		if setupParser == nil {
			parsingCfg := cfg.Parsing
			if len(parsingCfg.Engines) == 0 {
				parsingCfg.Engines = []string{"builtin"}
			}
			setupParser = parser.SetupParser(parsingCfg, doc.FileType)
		}
		if setupParser == nil {
			return domain.ErrParserUnsupported
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
			Message: fmt.Sprintf("处理 %d 张图片", len(parsedContent.Images)),
		})
		imageKeys := make([]string, len(parsedContent.Images))
		for i, image := range parsedContent.Images {
			imageKeys[i] = fmt.Sprintf("knowledge/%s/%s/images/%d", doc.KBID, doc.ID, image.Index)
		}
		doc.Metadata["image_keys"] = imageKeys
		if err := p.DocRepo.Update(ctx, doc); err != nil {
			return errors.Wrap(errors.CodeDBError, "failed to persist document image keys", err)
		}
		for i := range parsedContent.Images {
			img := &parsedContent.Images[i]
			key := imageKeys[i]
			if err := p.FileStore.Put(ctx, key, bytes.NewReader(img.RawContent), int64(len(img.RawContent)), img.ContentType); err != nil {
				return errors.Wrap(errors.CodeIOError, "failed to upload document image", err)
			}
			img.MinioKey = key
		}
		// VLM transcription via llm-gateway
		if cfg.Parsing.VLM != nil && cfg.Parsing.VLM.Enabled {
			if cfg.Parsing.VLM.ModelID == nil {
				return errors.New(errors.CodeInvalidParam, "VLM model is not configured")
			}
			if p.LLMGatewayClient == nil {
				return errors.New(errors.CodeServiceDown, "VLM gateway is not configured")
			}
			conn := p.LLMGatewayClient.Conn()
			if conn == nil {
				return errors.New(errors.CodeServiceDown, "VLM gateway is unavailable")
			}
			if conn != nil {
				cli := llmgateway.NewLLMGatewayClient(conn)
				// Serial image requests keep total VLM concurrency bounded by the
				// document worker pool instead of spawning a goroutine per image.
				for _, img := range parsedContent.Images {
					if len(img.RawContent) == 0 || img.URL == "" {
						continue
					}
					mime := img.ContentType
					if mime == "" {
						mime = "image/png"
					}
					dataURL := fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(img.RawContent))
					resp, err := cli.VlmChat(ctx, &llmgateway.VlmChatReq{
						ModelId: *cfg.Parsing.VLM.ModelID, OwnerId: ownerID,
						UserPrompt: "Please describe the content of this image in detail, including any text, data, tables, charts, and other information.",
						ImageData:  dataURL, MaxTokens: 1024,
					})
					if err != nil {
						return errors.Wrap(errors.CodeRPCError, "vlm describe failed", err)
					}
					if len(resp.Choices) > 0 {
						if resp.Choices[0].Message == nil {
							return errors.New(errors.CodeRPCError, "VLM returned an empty message")
						}
						orig := fmt.Sprintf("![%s](%s)", img.AltText, img.URL)
						figure := fmt.Sprintf("<figure>\n<img src=\"%s\" alt=\"%s\">\n<figcaption>%s</figcaption>\n</figure>", img.URL, img.AltText, resp.Choices[0].Message.Content)
						parsedContent.RawText = strings.ReplaceAll(parsedContent.RawText, orig, figure)
					} else {
						return errors.New(errors.CodeRPCError, "VLM returned no description")
					}
				}
			}
		}
	}

	// Stage 2: Chunk
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeChunking, Level: event.EventLevelInfo, Title: "正在分块", Message: "文档正在拆分为知识片段"})
	var pcResult *domain.ParentChildChunks
	var parentRecords, records []domain.ChunkRecord
	chunksPrepared := false
	if err := p.runStage(ctx, doc, domain.DocStatusChunking, func() error {
		if pcResult == nil {
			setupChunker := p.Chunker
			if setupChunker == nil {
				setupChunker = chunker.NewChunker()
			}
			chunks, err := setupChunker.Chunk(ctx, parsedContent, cfg.Chunking)
			if err != nil {
				return err
			}
			pcResult = chunks
		}
		if !chunksPrepared {
			parentIDMap := make(map[int]string, len(pcResult.Parents))
			for i := range pcResult.Parents {
				parent := &pcResult.Parents[i]
				if parent.ID == "" {
					id, err := identity.New()
					if err != nil {
						return err
					}
					parent.ID = id
				}
				parent.KBID, parent.DocID = doc.KBID, doc.ID
				parentIDMap[parent.Index] = parent.ID
			}
			for i := range pcResult.Children {
				child := &pcResult.Children[i]
				if child.ID == "" {
					id, err := identity.New()
					if err != nil {
						return err
					}
					child.ID = id
				}
				child.KBID, child.DocID = doc.KBID, doc.ID
				if child.Metadata == nil {
					child.Metadata = make(map[string]any)
				}
				child.Metadata["doc_title"] = doc.Title
				child.Metadata["doc_id"] = doc.ID
				child.Metadata["chunk_index"] = int32(child.Index)
				if len(pcResult.Parents) > 0 {
					parentIndex, ok := child.Metadata["parent_index"].(int)
					parentID, exists := parentIDMap[parentIndex]
					if !ok || !exists {
						return errors.New(errors.CodeInternal, "child chunk is missing its parent")
					}
					child.Metadata["parent_chunk_id"] = parentID
				}
			}
			parentRecords = make([]domain.ChunkRecord, len(pcResult.Parents))
			for i, parent := range pcResult.Parents {
				parentRecords[i] = domain.ChunkRecord{
					ID: parent.ID, DocID: parent.DocID, KBID: parent.KBID,
					ChunkIndex: parent.Index, Content: CleanInvalidUTF8(parent.Content),
					TokenCount: parent.TokenCount, Metadata: parent.Metadata,
				}
			}
			records = make([]domain.ChunkRecord, len(pcResult.Children))
			for i, child := range pcResult.Children {
				records[i] = domain.ChunkRecord{
					ID: child.ID, DocID: child.DocID, KBID: child.KBID,
					ChunkIndex: child.Index, Content: CleanInvalidUTF8(child.Content),
					TokenCount: child.TokenCount, Metadata: child.Metadata,
				}
				if parentID, ok := child.Metadata["parent_chunk_id"].(string); ok {
					records[i].ParentChunkID = &parentID
				}
			}
			chunksPrepared = true
		}

		if err := p.DocRepo.DeleteChunksByDoc(ctx, doc.ID); err != nil {
			return errors.Wrap(errors.CodeDBError, "failed to delete old chunks", err)
		}

		// A failed relationship write can have committed a prefix. Replace that
		// prefix on retry, reusing the IDs assigned before the first write.
		for i := range parentRecords {
			if err := p.DocRepo.CreateParentChunk(ctx, &parentRecords[i]); err != nil {
				return errors.Wrap(errors.CodeDBError, "failed to create parent chunk", err)
			}
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
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeEmbedding, Level: event.EventLevelInfo, Title: "正在向量化", Message: "文档正在生成向量"})
	// Retain completed batches if the final index write or a later batch needs
	// a stage retry; already embedded chunks must not consume another RPM slot.
	var completedVectors [][]float32
	vecDocs := make([]domain.VectorDoc, len(records))
	childTexts := make([]string, len(records))
	for i, record := range records {
		vecDocs[i] = domain.VectorDoc{
			ChunkID: record.ID, DocID: record.DocID, KBID: record.KBID,
			Content: record.Content, Metadata: record.Metadata,
		}
		childTexts[i] = record.Content
	}
	if err := p.runStage(ctx, doc, domain.DocStatusEmbedding, func() error {
		if len(childTexts) > 0 && p.Embedder == nil {
			return errors.New(errors.CodeServiceDown, "embedding gateway is not configured")
		}
		if len(vecDocs) > 0 && p.VectorStore == nil {
			return errors.New(errors.CodeServiceDown, "vector store is not configured")
		}

		if len(childTexts) > 0 && p.Embedder != nil {
			const batchSize = 10
			if completedVectors == nil {
				completedVectors = make([][]float32, len(childTexts))
			}
			vectors := completedVectors
			for start := 0; start < len(childTexts); start += batchSize {
				if vectors[start] != nil {
					continue
				}
				end := min(start+batchSize, len(childTexts))
				batch, err := p.Embedder.Embed(ctx, childTexts[start:end], embeddingModelID, ownerID)
				if err != nil {
					return errors.Wrap(errors.CodeRPCError, "embedding failed", err)
				}
				if len(batch) != end-start {
					return errors.New(errors.CodeRPCError, "embedding returned an unexpected vector count")
				}
				for _, vector := range batch {
					if len(vector) == 0 {
						return errors.New(errors.CodeRPCError, "embedding returned an empty vector")
					}
				}
				copy(vectors[start:], batch)
			}
			for i, vector := range vectors {
				vecDocs[i].Vector = vector
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
	p.emitProgress(ctx, doc, event.RealtimeEvent{Type: event.EventTypeKnowledgeReady, Level: event.EventLevelSuccess, Title: "入库完成", Message: "文档已完成入库"})
	l := p.Logger.WithContext(ctx)
	l.Infof("ingest pipeline completed: doc_id=%s stages=3 duration=%s", doc.ID, time.Since(start))
	return nil
}

func (p *IngestPipeline) clearChunks(ctx context.Context, docID string) error {
	var vectorErr error
	if p.VectorStore != nil {
		if err := p.VectorStore.DeleteByDoc(ctx, docID); err != nil {
			vectorErr = errors.Wrap(errors.CodeInternal, "failed to delete document vectors", err)
		}
	}
	var relationErr error
	if err := p.DocRepo.DeleteChunksByDoc(ctx, docID); err != nil {
		relationErr = errors.Wrap(errors.CodeDBError, "failed to delete document chunks", err)
	}
	return stderrors.Join(vectorErr, relationErr)
}

func (p *IngestPipeline) clearImages(ctx context.Context, doc *domain.Document) error {
	var keys []string
	switch value := doc.Metadata["image_keys"].(type) {
	case nil:
		return nil
	case []string:
		keys = value
	case []any:
		keys = make([]string, len(value))
		for i, item := range value {
			key, ok := item.(string)
			if !ok {
				return errors.New(errors.CodeInvalidParam, "invalid document image keys")
			}
			keys[i] = key
		}
	default:
		return errors.New(errors.CodeInvalidParam, "invalid document image keys")
	}
	prefix := fmt.Sprintf("knowledge/%s/%s/images/", doc.KBID, doc.ID)
	for _, key := range keys {
		index, ok := strings.CutPrefix(key, prefix)
		if !ok || index == "" || strings.Contains(index, "/") {
			return errors.New(errors.CodeInvalidParam, "image key does not belong to document")
		}
		for _, digit := range index {
			if digit < '0' || digit > '9' {
				return errors.New(errors.CodeInvalidParam, "invalid document image index")
			}
		}
	}
	var cleanupErr error
	for _, key := range keys {
		if err := p.FileStore.Delete(ctx, key); err != nil {
			cleanupErr = stderrors.Join(cleanupErr, errors.Wrap(errors.CodeIOError, "failed to delete document image", err))
		}
	}
	if cleanupErr == nil {
		delete(doc.Metadata, "image_keys")
	}
	return cleanupErr
}

func (p *IngestPipeline) runStage(ctx context.Context, doc *domain.Document, status domain.DocStatus, fn func() error) (err error) {
	l := p.Logger.WithContext(ctx)

	if err := p.DocRepo.UpdateStatus(ctx, doc.ID, status, ""); err != nil {
		return err
	}
	doc.Status = status
	doc.ErrorMessage = ""
	stageIndex := len(doc.Stages)
	doc.Stages = append(doc.Stages, domain.Stage{Name: string(status), Status: "running", StartedAt: time.Now().UnixMilli()})
	defer func() {
		stage := &doc.Stages[stageIndex]
		stage.EndedAt = time.Now().UnixMilli()
		stage.Status = "ready"
		if err != nil {
			stage.Status = "failed"
			stage.Error = err.Error()
		}
		stageCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if updateErr := p.DocRepo.UpdateStages(stageCtx, doc.ID, doc.Stages); updateErr != nil {
			err = stderrors.Join(err, errors.Wrap(errors.CodeDBError, "failed to update document stages", updateErr))
		}
	}()
	if err := p.DocRepo.UpdateStages(ctx, doc.ID, doc.Stages); err != nil {
		return errors.Wrap(errors.CodeDBError, "failed to update document stages", err)
	}

	limit := p.RetryLimit
	if limit <= 0 {
		limit = 3
	}

	var lastErr error
	for attempt := 0; attempt <= limit; attempt++ {
		doc.Stages[stageIndex].Retries = attempt
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
			l.Infof("stage %s attempt %d/%d failed: %v", string(status), attempt+1, limit+1, err)
		}
		if attempt == limit {
			break
		}
		backoff := time.Duration(attempt+1) * time.Second
		if attempt > 0 {
			backoff = time.Duration(1<<uint(attempt)) * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
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

downloads:
	for _, m := range matches {
		alt, url := m[1], m[2]
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break downloads
		}
		wg.Go(func() {
			defer func() { <-sem }()
			body, ct, err := downloadImage(ctx, url)
			if err != nil {
				logger.WithContext(ctx).Infof("download image %s failed: %v", url, err)
				return
			}
			results <- imgResult{AltText: alt, URL: url, RawContent: body, ContentType: ct}
		})
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
