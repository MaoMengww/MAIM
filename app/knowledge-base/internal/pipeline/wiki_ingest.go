package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	neo4j "github.com/maomeng/aim/app/knowledge-base/internal/infra/neo4j"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
)

type LLMChatRequest struct {
	Model    string         `json:"model"`
	ModelId  int64          `json:"model_id"`
	Messages []*LLMMessage  `json:"messages"`
	Params   map[string]any `json:"params,omitempty"`
	OwnerID  int64          `json:"owner_id"`
}

type LLMChatResponse struct {
	Content string `json:"content"`
	Model   string `json:"model"`
}

type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMGateway interface {
	Chat(ctx context.Context, req *LLMChatRequest) (*LLMChatResponse, error)
}

type WikiIngestPipeline struct {
	WikiRepo   domain.WikiPageRepo
	Neo4jStore neo4j.GraphStore
	FileStore  domain.FileStore
	DocRepo    domain.DocumentRepo
	KBRepo     domain.KBRepo
	LLMGateway LLMGateway
	Snowflake  *snowflake.Node
	Logger     logx.Logger
	Progress   func(ctx context.Context, docID int64, evt event.RealtimeEvent)
	LogWriter  *LogWriter
}

func (p *WikiIngestPipeline) emitProgress(ctx context.Context, docID int64, evt event.RealtimeEvent) {
	if p.Progress != nil {
		p.Progress(ctx, docID, evt)
	}
}

type wikiEntity struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	DocIDs      []int64  `json:"doc_ids"`
}

type wikiConcept struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	DocIDs      []int64  `json:"doc_ids"`
}

type docInfo struct {
	ID    int64
	Title string
}

func NewWikiIngestPipeline(
	graphStore neo4j.GraphStore,
	wikiRepo domain.WikiPageRepo, fileStore domain.FileStore,
	docRepo domain.DocumentRepo, kbRepo domain.KBRepo,
	llmGW LLMGateway, snow *snowflake.Node, logger logx.Logger,
) *WikiIngestPipeline {
	return &WikiIngestPipeline{
		WikiRepo: wikiRepo, FileStore: fileStore,
		DocRepo: docRepo, KBRepo: kbRepo,
		LLMGateway: llmGW, Snowflake: snow, Logger: logger,
		Neo4jStore: graphStore,
	}
}

func (p *WikiIngestPipeline) Start(ctx context.Context, kbID int64, docIDs []int64) error {
	start := time.Now()
	logger := p.Logger.WithContext(ctx)
	logger.Infof("wiki ingest start: kb=%d docs=%v", kbID, docIDs)
	defer func() {
		logger.Infof("wiki ingest done: kb=%d docs=%d duration=%s", kbID, len(docIDs), time.Since(start))
	}()

	// 1. Read document metadata
	var docs []*domain.Document
	var docInfos []docInfo
	for _, id := range docIDs {
		doc, err := p.DocRepo.Get(ctx, id)
		if err != nil {
			logger.Errorf("get doc %d failed: %v", id, err)
			continue
		}
		docs = append(docs, doc)
		docInfos = append(docInfos, docInfo{ID: doc.ID, Title: doc.Title})
	}
	if len(docs) == 0 {
		return nil
	}

	// 2. Get KB info, existing slugs for continuity, model config
	kb, err := p.KBRepo.Get(ctx, kbID)
	if err != nil {
		return fmt.Errorf("get kb %d: %w", kbID, err)
	}
	modelID := kb.PipelineConfig.Wiki.ModelID
	if modelID == 0 {
		modelID = 14 // default: qwen-plus
	}
	ownerID := kb.OwnerID
	previousSlugs := p.getPreviousSlugs(ctx, kbID)
	language := "Chinese"

	// 3. Pass 0: Per-document candidate extraction (concurrent, semaphore 5)
	type candResult struct {
		docID    int64
		entities []wikiEntity
		concepts []wikiConcept
		err      error
	}
	sem := make(chan struct{}, 5)
	candCh := make(chan candResult, len(docs))

	for _, doc := range docs {
		go func(d *domain.Document) {
			sem <- struct{}{}
			defer func() { <-sem }()
			ents, concs, err := p.extractCandidates(ctx, kbID, d, language, previousSlugs)
			candCh <- candResult{docID: d.ID, entities: ents, concepts: concs, err: err}
		}(doc)
	}

	var allEntities []wikiEntity
	var allConcepts []wikiConcept
	for range docs {
		r := <-candCh
		if r.err != nil {
			logger.Errorf("extract candidates for doc %d: %v", r.docID, r.err)
			continue
		}
		allEntities = append(allEntities, r.entities...)
		allConcepts = append(allConcepts, r.concepts...)
	}

	// 4. Dedup
	allEntities = dedupEntities(allEntities)
	allConcepts = dedupConcepts(allConcepts)

	entityNames := make(map[string]bool)
	for _, e := range allEntities {
		entityNames[strings.ToLower(strings.TrimSpace(e.Name))] = true
	}
	deduped := allConcepts[:0]
	for _, c := range allConcepts {
		if !entityNames[strings.ToLower(strings.TrimSpace(c.Name))] {
			deduped = append(deduped, c)
		}
	}
	allConcepts = deduped

	// 5. Reduce merge or create new pages (serial, write consistency)
	now := time.Now()
	var pages []*domain.WikiPage

	for _, ent := range allEntities {
		slug := "entity/" + slugify(ent.Name)
		existing, err := p.WikiRepo.GetBySlug(ctx, kbID, slug)
		if err == nil {
			// Merge new info into existing page
			if err := p.reduceMerge(ctx, kbID, existing, docInfos, nil, language); err != nil {
				logger.Errorf("reduceMerge entity %s failed: %v", slug, err)
			}
		} else {
			pageID, err := p.Snowflake.Generate()
				if err != nil {
					return fmt.Errorf("generate page id failed: %w", err)
				}
				pages = append(pages, &domain.WikiPage{
				ID: pageID, KnowledgeBaseID: kbID,
				Slug: slug, Title: ent.Name,
				PageType: domain.WikiPageEntity, Status: domain.WikiPagePublished,
				Content: ent.Description, Aliases: toRawJSON(ent.Aliases),
				SourceRefs: toRawJSON(buildDocRefs(docInfos)),
				Version:    1, CreatedAt: now, UpdatedAt: now,
			})
		}
	}

	for _, cpt := range allConcepts {
		slug := "concept/" + slugify(cpt.Name)
		existing, err := p.WikiRepo.GetBySlug(ctx, kbID, slug)
		if err == nil {
			if err := p.reduceMerge(ctx, kbID, existing, docInfos, nil, language); err != nil {
				logger.Errorf("reduceMerge concept %s failed: %v", slug, err)
			}
		} else {
			pageID, err := p.Snowflake.Generate()
			if err != nil {
				return fmt.Errorf("generate page id failed: %w", err)
			}
			pages = append(pages, &domain.WikiPage{
				ID: pageID, KnowledgeBaseID: kbID,
				Slug: slug, Title: cpt.Name,
				PageType: domain.WikiPageConcept, Status: domain.WikiPagePublished,
				Content: cpt.Description, Aliases: toRawJSON(cpt.Aliases),
				SourceRefs: toRawJSON(buildDocRefs(docInfos)),
				Version:    1, CreatedAt: now, UpdatedAt: now,
			})
		}
	}

	// 6. Per-document summaries
	for _, doc := range docs {
		text, err := p.readFileContent(ctx, doc.MinioKey)
		if err != nil {
			continue
		}
		summary, _ := p.callLLM(ctx, modelID, ownerID, buildSummaryPrompt(doc.Title, text))
		if summary == "" {
			continue
		}
		s := truncate(summary, 200)
		slug := "summary/doc-" + slugify(doc.Title)
		pageID, err := p.Snowflake.Generate()
			if err != nil {
				return fmt.Errorf("generate page id failed: %w", err)
			}
			pages = append(pages, &domain.WikiPage{
			ID: pageID, KnowledgeBaseID: kbID,
			Slug: slug, Title: doc.Title + " 摘要",
			PageType: domain.WikiPageSummary, Status: domain.WikiPagePublished,
			Content: summary, Summary: s,
			SourceRefs: toRawJSON(buildDocRefs([]docInfo{{ID: doc.ID, Title: doc.Title}})),
			Version:    1, CreatedAt: now, UpdatedAt: now,
		})
	}

	// 7. Global LLM analysis (synthesis, comparison)
	fullText := p.concatAllDocTexts(ctx, docs)
	synthesisText, _ := p.callLLM(ctx, modelID, ownerID, buildSynthesisPrompt(fullText))
	comparisonText, _ := p.callLLM(ctx, modelID, ownerID, buildComparisonPrompt(fullText))

	var synthesisItems []wikiSynthesisItem
	if synthesisText != "" {
		_ = json.Unmarshal([]byte(synthesisText), &synthesisItems)
	}
	for _, item := range synthesisItems {
		slug := "synthesis/" + slugify(item.Slug)
		title := item.Title
		if title == "" {
			title = kb.Name + " 综合论述"
		}
		pageID, err := p.Snowflake.Generate()
		if err != nil {
			return fmt.Errorf("generate page id failed: %w", err)
		}
		pages = append(pages, &domain.WikiPage{
			ID: pageID, KnowledgeBaseID: kbID,
			Slug: slug, Title: title,
			PageType: domain.WikiPageSynthesis, Status: domain.WikiPagePublished,
			Content:    item.Content,
			SourceRefs: toRawJSON(buildDocRefs(docInfos)),
			Version:    1, CreatedAt: now, UpdatedAt: now,
		})
	}

	var comparisonItems []wikiComparisonItem
	if comparisonText != "" {
		_ = json.Unmarshal([]byte(comparisonText), &comparisonItems)
	}
	for _, item := range comparisonItems {
		slug := "comparison/" + slugify(item.Slug)
		title := item.Title
		if title == "" {
			title = kb.Name + " 对比分析"
		}
		pageID, err := p.Snowflake.Generate()
		if err != nil {
			return fmt.Errorf("generate page id failed: %w", err)
		}
		pages = append(pages, &domain.WikiPage{
			ID: pageID, KnowledgeBaseID: kbID,
			Slug: slug, Title: title,
			PageType: domain.WikiPageComparison, Status: domain.WikiPagePublished,
			Content:    item.Content,
			SourceRefs: toRawJSON(buildDocRefs(docInfos)),
			Version:    1, CreatedAt: now, UpdatedAt: now,
		})
	}

	// 8. Upsert all new pages
	for _, page := range pages {
		if err := p.WikiRepo.Upsert(ctx, page); err != nil {
			logger.Errorf("wiki upsert %s failed: %v", page.Slug, err)
		}
	}

	// 9. Update index, cross-links, Neo4j, log
	p.updateIndexPage(ctx, kbID)
	p.injectCrossLinks(ctx, kbID)
	p.linkify(ctx, kbID)
	if p.LogWriter != nil {
		var titles []string
		for _, d := range docInfos {
			titles = append(titles, d.Title)
		}
		detail := generateLogSummary(ctx, p.LLMGateway, modelID, ownerID, "文档导入", strings.Join(titles, "、"))
		_ = p.LogWriter.Write(ctx, kbID, "文档导入", detail)
	}
	p.syncToNeo4j(ctx, kbID)

	// 10. Mark docs as ready and emit events
	for _, di := range docInfos {
		_ = p.DocRepo.UpdateStatus(ctx, di.ID, domain.DocStatusReady, "")
		p.emitProgress(ctx, di.ID, event.RealtimeEvent{
			Type:    event.EventTypeKnowledgeReady,
			Level:   event.EventLevelSuccess,
			Title:   "Wiki 处理完成",
			Message: fmt.Sprintf("文档「%s」已完成 Wiki 解析", di.Title),
		})
	}

	logger.Infof("wiki ingest done: kb=%d docs=%d pages=%d duration=%s",
		kbID, len(docIDs), len(pages), time.Since(start))
	return nil
}

func (p *WikiIngestPipeline) readFileContent(ctx context.Context, key string) (string, error) {
	r, err := p.FileStore.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer r.Close()
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, r); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (p *WikiIngestPipeline) callLLM(ctx context.Context, modelID int64, ownerID int64, prompt string) (string, error) {
	resp, err := p.LLMGateway.Chat(ctx, &LLMChatRequest{
		ModelId: modelID,
		OwnerID: ownerID,
		Messages: []*LLMMessage{
			{Role: "system", Content: "You are a professional knowledge base analysis assistant."},
			{Role: "user", Content: prompt},
		},
		Params: map[string]any{"temperature": 0.3},
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (p *WikiIngestPipeline) linkify(ctx context.Context, kbID int64) {
	allPages, err := p.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil {
		return
	}

	slugSet := make(map[string]bool)
	for _, pg := range allPages {
		slugSet[pg.Slug] = true
	}

	linkRe := regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

	// Collect out_links from every page's content
	outLinkMap := make(map[string][]string, len(allPages))
	for _, page := range allPages {
		matches := linkRe.FindAllStringSubmatch(page.Content, -1)
		var outLinks []string
		seen := make(map[string]bool)
		for _, m := range matches {
			slug := m[1]
			if slugSet[slug] && !seen[slug] {
				// Handle pipe syntax: [[slug|title]] -> slug
				if idx := strings.Index(slug, "|"); idx >= 0 {
					slug = slug[:idx]
				}
				outLinks = append(outLinks, slug)
				seen[slug] = true
			}
		}
		outLinkMap[page.Slug] = outLinks
	}

	// Compute in_links from out_links
	inLinkMap := make(map[string][]string, len(allPages))
	for slug, outLinks := range outLinkMap {
		for _, target := range outLinks {
			inLinkMap[target] = append(inLinkMap[target], slug)
		}
	}

	// Write all links back
	for _, page := range allPages {
		p.WikiRepo.UpdateOutLinks(ctx, kbID, page.Slug, outLinkMap[page.Slug])
		p.WikiRepo.UpdateInLinks(ctx, kbID, page.Slug, inLinkMap[page.Slug])
	}
}

func (p *WikiIngestPipeline) injectCrossLinks(ctx context.Context, kbID int64) {
	logger := p.Logger.WithContext(ctx)
	allPages, err := p.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil || len(allPages) == 0 {
		return
	}

	type linkRef struct {
		slug  string
		match string
	}
	var refs []linkRef
	for _, pg := range allPages {
		if pg.PageType != domain.WikiPageEntity && pg.PageType != domain.WikiPageConcept &&
			pg.PageType != domain.WikiPageSynthesis && pg.PageType != domain.WikiPageComparison &&
			pg.PageType != domain.WikiPageSummary {
			continue
		}
		title := strings.TrimSpace(pg.Title)
		if utf8.RuneCountInString(title) >= 2 {
			refs = append(refs, linkRef{pg.Slug, title})
		}
		var aliases []string
		if len(pg.Aliases) > 0 {
			json.Unmarshal(pg.Aliases, &aliases)
			for _, a := range aliases {
				a = strings.TrimSpace(a)
				if utf8.RuneCountInString(a) >= 2 {
					refs = append(refs, linkRef{pg.Slug, a})
				}
			}
		}
	}
	if len(refs) == 0 {
		return
	}

	// Sort by match length descending (longer matches first to avoid substring issues)
	sort.Slice(refs, func(i, j int) bool {
		return utf8.RuneCountInString(refs[i].match) > utf8.RuneCountInString(refs[j].match)
	})

	// Forbidden regions: existing [[...]], fenced code blocks, inline code
	forbiddenRe := regexp.MustCompile("```[^`]*```|`[^`]*`|\\[\\[[^\\]]+\\]\\]")

	for _, page := range allPages {
		content := page.Content
		if content == "" {
			continue
		}
		modified := false

		// Compute forbidden regions
		forbid := forbiddenRe.FindAllStringIndex(content, -1)

		for _, ref := range refs {
			if ref.slug == page.Slug {
				continue // no self-links
			}
			// Skip if already explicitly linked
			if strings.Contains(content, "[["+ref.slug+"]]") {
				continue
			}

			// Find first safe occurrence
			searchStart := 0
			for {
				idx := strings.Index(content[searchStart:], ref.match)
				if idx < 0 {
					break
				}
				absIdx := searchStart + idx

				// Check if inside forbidden region
				insideForbidden := false
				for _, f := range forbid {
					if absIdx >= f[0] && absIdx < f[1] {
						insideForbidden = true
						searchStart = f[1]
						break
					}
				}
				if insideForbidden {
					continue
				}

				// Check word boundary: ensure surrounding chars aren't similar
				byteEnd := absIdx + len(ref.match)
				boundaryOK := true
				if byteEnd < len(content) {
					nextRune, _ := utf8.DecodeRuneInString(content[byteEnd:])
					if unicode.IsLetter(nextRune) || unicode.IsDigit(nextRune) {
						boundaryOK = false
					}
				}
				if absIdx > 0 {
					prevRune, _ := utf8.DecodeLastRuneInString(content[:absIdx])
					if unicode.IsLetter(prevRune) || unicode.IsDigit(prevRune) {
						boundaryOK = false
					}
				}

				if !boundaryOK {
					searchStart = absIdx + len(ref.match)
					continue
				}

				// Found a safe match - replace with [[slug]]
				replacement := "[[" + ref.slug + "]]"
				content = content[:absIdx] + replacement + content[byteEnd:]
				modified = true

				// Update forbidden regions to include the new link
				newEnd := absIdx + len(replacement)
				forbid = append(forbid, []int{absIdx, newEnd})
				break // one replacement per ref per page
			}
		}

		if modified {
			page.Content = content
			if err := p.WikiRepo.Upsert(ctx, &page); err != nil {
				logger.Errorf("crosslink upsert %s failed: %v", page.Slug, err)
			}
		}
	}
}
func (p *WikiIngestPipeline) updateIndexPage(ctx context.Context, kbID int64) {
	pages, err := p.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil {
		return
	}
	var sb strings.Builder
	sb.WriteString("# 知识库索引\n\n此页面由系统自动维护。\n\n---\n\n")
	byType := make(map[domain.WikiPageType][]domain.WikiPage)
	for _, pg := range pages {
		if pg.PageType == domain.WikiPageIndex || pg.PageType == domain.WikiPageLog {
			continue
		}
		byType[pg.PageType] = append(byType[pg.PageType], pg)
	}
	for _, pt := range []domain.WikiPageType{
		domain.WikiPageSummary, domain.WikiPageEntity,
		domain.WikiPageConcept, domain.WikiPageSynthesis,
		domain.WikiPageComparison,
	} {
		list, ok := byType[pt]
		if !ok {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %s (%d)\n\n", pageTypeLabel(pt), len(list)))
		for _, pg := range list {
			if pg.Summary != "" {
				// 摘要过长时截断，保持索引简洁
				summary := pg.Summary
				runes := []rune(summary)
				if len(runes) > 100 {
					summary = string(runes[:100]) + "…"
				}
				sb.WriteString(fmt.Sprintf("- [[%s]] %s\n", pg.Slug, summary))
			} else {
				sb.WriteString(fmt.Sprintf("- [[%s]]\n", pg.Slug))
			}
		}
		sb.WriteString("\n")
	}
	// List archive log pages
	var logArchives []domain.WikiPage
	for _, pg := range pages {
		if pg.PageType == domain.WikiPageLog && pg.Slug != "log" {
			logArchives = append(logArchives, pg)
		}
	}
	if len(logArchives) > 0 {
		sb.WriteString(fmt.Sprintf("## 历史日志 (%d)\n\n", len(logArchives)))
		for _, pg := range logArchives {
			sb.WriteString(fmt.Sprintf("- [[%s]]\n", pg.Slug))
		}
		sb.WriteString("\n")
	}
	pageID, err := p.Snowflake.Generate()
	if err != nil {
		p.Logger.Error("generate index page id failed: %v", err)
		return
	}
	p.WikiRepo.Upsert(ctx, &domain.WikiPage{
		ID: pageID, KnowledgeBaseID: kbID,
		Slug: "index", Title: "知识库索引",
		PageType: domain.WikiPageIndex, Status: domain.WikiPagePublished,
		Content: sb.String(), Version: 1,
	})
}

// LogWriter 统一的变更日志写入器
const logMaxEntries = 100

type LogWriter struct {
	WikiRepo  domain.WikiPageRepo
	Snowflake *snowflake.Node
}

func (w *LogWriter) Write(ctx context.Context, kbID int64, action, detail string) error {
	existing, err := w.WikiRepo.GetBySlug(ctx, kbID, "log")
	content := ""
	if err == nil && existing != nil {
		content = existing.Content
	}
	// Check threshold: count entries (### 开头)
	if strings.Count(content, "\n### ") >= logMaxEntries {
		archiveSlug := "log-" + time.Now().Format("2006-01-02")
		entryID, err := w.Snowflake.Generate()
		if err != nil {
			// skip archive on id generation failure
		} else {
			_ = w.WikiRepo.Upsert(ctx, &domain.WikiPage{
				ID: entryID, KnowledgeBaseID: kbID,
				Slug: archiveSlug, Title: "变更日志 " + time.Now().Format("2006-01-02"),
				PageType: domain.WikiPageLog, Status: domain.WikiPagePublished,
				Content: content, Version: 1,
			})
		}
		content = ""
	}
	entry := fmt.Sprintf("### %s\n\n**%s**: %s\n\n", time.Now().Format("2006-01-02 15:04:05"), action, detail)
	entryID, err := w.Snowflake.Generate()
	if err != nil {
		return fmt.Errorf("generate log entry id failed: %w", err)
	}
	if err := w.WikiRepo.Upsert(ctx, &domain.WikiPage{
		ID: entryID, KnowledgeBaseID: kbID,
		Slug: "log", Title: "变更日志",
		PageType: domain.WikiPageLog, Status: domain.WikiPagePublished,
		Content: entry + content, Version: 1,
	}); err != nil {
		return err
	}
	return nil
}

func (p *WikiIngestPipeline) syncToNeo4j(ctx context.Context, kbID int64) {
	logger := p.Logger.WithContext(ctx)
	pages, err := p.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil || len(pages) == 0 {
		return
	}
	for _, page := range pages {
		if err := p.Neo4jStore.SyncNode(ctx, kbID, page.Slug, page.Title, string(page.PageType)); err != nil {
			logger.Errorf("neo4j sync node %s failed: %v", page.Slug, err)
		}
	}
	for _, page := range pages {
		var outLinks []string
		if len(page.OutLinks) > 0 {
			json.Unmarshal(page.OutLinks, &outLinks)
		}
		if err := p.Neo4jStore.SyncRelationships(ctx, kbID, page.Slug, outLinks); err != nil {
			logger.Errorf("neo4j sync relationships %s failed: %v", page.Slug, err)
		}
	}
	logger.Infof("neo4j sync done: kb=%d nodes=%d", kbID, len(pages))
}

// getPreviousSlugs returns a newline-separated list of existing page slugs for slug continuity
func (p *WikiIngestPipeline) getPreviousSlugs(ctx context.Context, kbID int64) string {
	pages, err := p.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil {
		return ""
	}
	var slugs []string
	for _, pg := range pages {
		slugs = append(slugs, pg.Slug)
	}
	return strings.Join(slugs, "\n")
}

// extractCandidates performs Pass 0: lightweight per-document candidate extraction.
// Returns entity and concept candidates with doc IDs.
func (p *WikiIngestPipeline) extractCandidates(ctx context.Context, kbID int64, doc *domain.Document, language string, previousSlugs string) ([]wikiEntity, []wikiConcept, error) {
	text, err := p.readFileContent(ctx, doc.MinioKey)
	if err != nil {
		return nil, nil, err
	}

	modelID := p.getModelID(kbID)
	prompt := buildCandidateExtractionPrompt(text, previousSlugs, language)
	resp, err := p.callLLM(ctx, modelID, p.getOwnerID(kbID), prompt)
	if err != nil || resp == "" {
		return nil, nil, err
	}

	var result struct {
		Entities []wikiEntity  `json:"entities"`
		Concepts []wikiConcept `json:"concepts"`
	}
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, nil, err
	}
	// Tag all extracted items with the current doc ID
	for i := range result.Entities {
		result.Entities[i].DocIDs = []int64{doc.ID}
	}
	for i := range result.Concepts {
		result.Concepts[i].DocIDs = []int64{doc.ID}
	}
	return result.Entities, result.Concepts, nil
}

// reduceMerge performs REDUCE: merge new document content into an existing wiki page.
func (p *WikiIngestPipeline) reduceMerge(ctx context.Context, kbID int64, page *domain.WikiPage, newDocs []docInfo, deletedDocIDs []int64, language string) error {
	modelID := p.getModelID(kbID)

	var additions []string
	for _, d := range newDocs {
		doc, rerr := p.DocRepo.Get(ctx, d.ID)
		if rerr != nil {
			continue
		}
		text, rerr := p.readFileContent(ctx, doc.MinioKey)
		if rerr != nil {
			continue
		}
		additions = append(additions, fmt.Sprintf("<doc id=\"%d\">\n%s\n</doc>", d.ID, text))
	}

	prompt := buildReduceMergePrompt(page, additions, deletedDocIDs, language)
	resp, err := p.callLLM(ctx, modelID, p.getOwnerID(kbID), prompt)
	if err != nil || resp == "" {
		return err
	}

	// LLM returns: SUMMARY: ...\n\nMarkdown content
	lines := strings.SplitN(resp, "\n", 2)
	if len(lines) == 2 {
		page.Content = strings.TrimSpace(lines[1])
		page.Summary = strings.TrimPrefix(lines[0], "SUMMARY: ")
	}

	// Append new source refs to existing page
	if len(newDocs) > 0 {
		var existingRefs []map[string]any
		if len(page.SourceRefs) > 0 {
			json.Unmarshal(page.SourceRefs, &existingRefs)
		}
		existingIDs := make(map[int64]bool)
		for _, r := range existingRefs {
			if id, ok := r["doc_id"].(float64); ok {
				existingIDs[int64(id)] = true
			}
		}
		for _, d := range newDocs {
			if !existingIDs[d.ID] {
				existingRefs = append(existingRefs, map[string]any{"doc_id": d.ID, "title": d.Title})
			}
		}
		page.SourceRefs = toRawJSON(existingRefs)
	}

	page.Version++
	return p.WikiRepo.Upsert(ctx, page)
}

// dedupEntities deduplicates entities by name (case-insensitive).
func dedupEntities(entities []wikiEntity) []wikiEntity {
	seen := make(map[string]bool)
	var result []wikiEntity
	for _, e := range entities {
		key := strings.ToLower(strings.TrimSpace(e.Name))
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, e)
	}
	return result
}

// dedupConcepts deduplicates concepts by name (case-insensitive).
func dedupConcepts(concepts []wikiConcept) []wikiConcept {
	seen := make(map[string]bool)
	var result []wikiConcept
	for _, c := range concepts {
		key := strings.ToLower(strings.TrimSpace(c.Name))
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, c)
	}
	return result
}

// getModelID returns the model ID for the KB, falling back to default.
func (p *WikiIngestPipeline) getModelID(kbID int64) int64 {
	kb, err := p.KBRepo.Get(context.Background(), kbID)
	if err != nil {
		return 14 // default: qwen-plus
	}
	if kb.PipelineConfig.Wiki.ModelID == 0 {
		return 14
	}
	return kb.PipelineConfig.Wiki.ModelID
}

// getOwnerID returns the owner ID for the KB.
func (p *WikiIngestPipeline) getOwnerID(kbID int64) int64 {
	kb, err := p.KBRepo.Get(context.Background(), kbID)
	if err != nil {
		return 0
	}
	return kb.OwnerID
}

// concatAllDocTexts reads all docs from MinIO and concatenates with headers.
func (p *WikiIngestPipeline) concatAllDocTexts(ctx context.Context, docs []*domain.Document) string {
	var texts []string
	for _, doc := range docs {
		text, err := p.readFileContent(ctx, doc.MinioKey)
		if err != nil {
			continue
		}
		texts = append(texts, fmt.Sprintf("[文档: %s] (doc_id: %d)\n%s", doc.Title, doc.ID, text))
	}
	return strings.Join(texts, "\n\n---\n\n")
}

func slugify(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func buildDocRefs(docInfos []docInfo) []map[string]any {
	refs := make([]map[string]any, len(docInfos))
	for i, d := range docInfos {
		refs[i] = map[string]any{"doc_id": d.ID, "title": d.Title}
	}
	return refs
}

func toRawJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func pageTypeLabel(pt domain.WikiPageType) string {
	switch pt {
	case domain.WikiPageSummary:
		return "概览"
	case domain.WikiPageEntity:
		return "实体"
	case domain.WikiPageConcept:
		return "概念"
	case domain.WikiPageSynthesis:
		return "综合论述"
	case domain.WikiPageComparison:
		return "对比分析"
	default:
		return string(pt)
	}
}
