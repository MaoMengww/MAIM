package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/eventpush"
	neo4j "github.com/maomeng/aim/app/knowledge-base/internal/infra/neo4j"
	"github.com/maomeng/aim/app/knowledge-base/internal/pipeline"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
)

type WikiHandler struct {
	WikiRepo      domain.WikiPageRepo
	DocRepo       domain.DocumentRepo
	FileStore     domain.FileStore
	KBRepo        domain.KBRepo
	Snowflake     *snowflake.Node
	WikiPipe      *pipeline.WikiIngestPipeline
	WikiSearch    *pipeline.WikiSearchPipeline
	WikiLint      *pipeline.WikiLintPipeline
	WikiMaintain  *pipeline.WikiMaintenanceAgent
	WikiAgent     *pipeline.WikiReActAgent
	WikiEinoAgent *pipeline.WikiEinoAgent
	Neo4jStore    neo4j.GraphStore
	Logger        logx.Logger
	LogWriter     *pipeline.LogWriter
	Pusher        eventpush.Pusher
}

// WikiPage RPC 响应辅助
type wikiPageRsp struct {
	ID         int64           `json:"id"`
	Slug       string          `json:"slug"`
	Title      string          `json:"title"`
	PageType   string          `json:"page_type"`
	Content    string          `json:"content"`
	Summary    string          `json:"summary"`
	Aliases    []string        `json:"aliases"`
	OutLinks   []string        `json:"out_links"`
	InLinks    []string        `json:"in_links"`
	Version    int32           `json:"version"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
	SourceRefs []wikiSourceRef `json:"source_refs,omitempty"`
}

type wikiSourceRef struct {
	DocID int64  `json:"doc_id"`
	Title string `json:"title"`
}

type wikiSourceDocRsp struct {
	DocID   int64  `json:"doc_id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (h *WikiHandler) ReadIndex(ctx context.Context, kbID int64) (*wikiPageRsp, error) {
	page, err := h.WikiRepo.GetBySlug(ctx, kbID, "index")
	if err != nil {
		return nil, domain.ErrWikiPageNotFound
	}
	return toWikiPageRsp(page), nil
}

func (h *WikiHandler) ReadPage(ctx context.Context, kbID int64, slug string) (*wikiPageRsp, error) {
	page, err := h.WikiRepo.GetBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, domain.ErrWikiPageNotFound
	}
	return toWikiPageRsp(page), nil
}

func (h *WikiHandler) ReadSourceDoc(ctx context.Context, kbID, docID int64, query string) (*wikiSourceDocRsp, error) {
	doc, err := h.DocRepo.Get(ctx, docID)
	if err != nil {
		return nil, domain.ErrDocNotFound
	}
	text, err := h.readFileContent(ctx, doc.MinioKey)
	if err != nil {
		return nil, err
	}
	return &wikiSourceDocRsp{
		DocID: doc.ID, Title: doc.Title, Content: truncateContent(text, 10000),
	}, nil
}

func (h *WikiHandler) readFileContent(ctx context.Context, key string) (string, error) {
	if key == "" || h.FileStore == nil {
		return "", nil
	}
	r, err := h.FileStore.Get(ctx, key)
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

func (h *WikiHandler) ListPages(ctx context.Context, kbID int64, pageType string) ([]*wikiPageItem, error) {
	var pages []domain.WikiPage
	var err error
	if pageType != "" {
		pages, err = h.WikiRepo.ListByKBAndType(ctx, kbID, domain.WikiPageType(pageType))
	} else {
		pages, err = h.WikiRepo.ListAllByKB(ctx, kbID)
	}
	if err != nil {
		return nil, err
	}
	items := make([]*wikiPageItem, len(pages))
	for i, p := range pages {
		items[i] = &wikiPageItem{
			ID: p.ID, Slug: p.Slug, Title: p.Title,
			PageType: string(p.PageType), Summary: p.Summary,
			Version: int32(p.Version), UpdatedAt: p.UpdatedAt.Unix(),
		}
	}
	return items, nil
}

type wikiPageItem struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	PageType  string `json:"page_type"`
	Summary   string `json:"summary"`
	Version   int32  `json:"version"`
	UpdatedAt int64  `json:"updated_at"`
}

func (h *WikiHandler) Search(ctx context.Context, kbID int64, query string, limit int) ([]*wikiSearchItem, error) {
	// 1) try LLM semantic search if wiki has a model configured
	kb, err := h.KBRepo.Get(ctx, kbID)
	if err == nil && kb.PipelineConfig.Wiki.ModelID > 0 {
		results, searchErr := h.WikiSearch.SearchByIndex(ctx, kbID, query, kb.PipelineConfig.Wiki.ModelID, kb.OwnerID, limit)
		if searchErr == nil && len(results) > 0 {
			items := make([]*wikiSearchItem, len(results))
			for i, r := range results {
				items[i] = &wikiSearchItem{
					Slug: r.Slug, Title: r.Title,
					PageType: r.PageType,
					Snippet:  truncateContent(r.Content, 200),
				}
			}
			return items, nil
		}
	}

	// 2) fallback: full-text search
	pages, ftErr := h.WikiRepo.FullTextSearch(ctx, kbID, query, limit)
	if ftErr != nil {
		return nil, ftErr
	}
	items := make([]*wikiSearchItem, len(pages))
	for i, p := range pages {
		items[i] = &wikiSearchItem{
			Slug: p.Slug, Title: p.Title,
			PageType: string(p.PageType),
			Snippet:  truncateContent(p.Content, 200),
		}
	}
	return items, nil
}

type wikiSearchItem struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	PageType string `json:"page_type"`
	Snippet  string `json:"snippet"`
}

func (h *WikiHandler) UpdatePage(ctx context.Context, kbID int64, slug, title, content, summary string, aliases []string) (*wikiPageRsp, error) {
	existing, err := h.WikiRepo.GetBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, domain.ErrWikiPageNotFound
	}
	if title != "" {
		existing.Title = title
	}
	if content != "" {
		existing.Content = content
	}
	existing.Summary = summary
	if len(aliases) > 0 {
		b, _ := json.Marshal(aliases)
		existing.Aliases = b
	}
	existing.Version++
	existing.UpdatedAt = time.Now()
	if err := h.WikiRepo.Upsert(ctx, existing); err != nil {
		return nil, err
	}
	if h.LogWriter != nil {
		_ = h.LogWriter.Write(ctx, kbID, "页面编辑", slug)
	}
	return toWikiPageRsp(existing), nil
}

func (h *WikiHandler) DeletePage(ctx context.Context, kbID int64, slug string) error {
	err := h.WikiRepo.SoftDelete(ctx, kbID, slug)
	if err == nil && h.LogWriter != nil {
		_ = h.LogWriter.Write(ctx, kbID, "页面删除", slug)
	}
	return err
}

func (h *WikiHandler) ReplaceText(ctx context.Context, kbID int64, slug, oldText, newText string) (*wikiPageRsp, error) {
	existing, err := h.WikiRepo.GetBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, domain.ErrWikiPageNotFound
	}
	if !strings.Contains(existing.Content, oldText) {
		return nil, fmt.Errorf("未找到匹配文本")
	}
	existing.Content = strings.ReplaceAll(existing.Content, oldText, newText)
	existing.Version++
	if err := h.WikiRepo.Upsert(ctx, existing); err != nil {
		return nil, err
	}
	return toWikiPageRsp(existing), nil
}

func (h *WikiHandler) RenamePage(ctx context.Context, kbID int64, slug, newSlug string) (*wikiPageRsp, error) {
	page, err := h.WikiRepo.GetBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, domain.ErrWikiPageNotFound
	}
	// Check new slug doesn't conflict
	if existing, _ := h.WikiRepo.GetBySlug(ctx, kbID, newSlug); existing != nil {
		return nil, fmt.Errorf("新 slug %s 已存在", newSlug)
	}
	oldSlug := page.Slug
	page.Slug = newSlug
	page.Version++
	if err := h.WikiRepo.Upsert(ctx, page); err != nil {
		return nil, err
	}
	// Cascade rename in all pages
	h.cascadeRename(ctx, kbID, oldSlug, newSlug)
	return toWikiPageRsp(page), nil
}

func (h *WikiHandler) cascadeRename(ctx context.Context, kbID int64, oldSlug, newSlug string) {
	pages, err := h.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil {
		return
	}
	oldBracket := "[[" + oldSlug + "]]"
	oldBar := "[[" + oldSlug + "|"
	newBracket := "[[" + newSlug + "]]"
	newBar := "[[" + newSlug + "|"
	for _, p := range pages {
		if !strings.Contains(p.Content, oldBracket) && !strings.Contains(p.Content, oldBar) {
			continue
		}
		p.Content = strings.ReplaceAll(p.Content, oldBracket, newBracket)
		p.Content = strings.ReplaceAll(p.Content, oldBar, newBar)
		p.Version++
		h.WikiRepo.Upsert(ctx, &p)
	}
}

func (h *WikiHandler) ListIssues(ctx context.Context, kbID int64, status string) ([]*wikiIssueItem, error) {
	issues, err := h.WikiRepo.ListIssuesByKB(ctx, kbID, status)
	if err != nil {
		return nil, err
	}
	items := make([]*wikiIssueItem, 0, len(issues))
	for _, iss := range issues {
		if status == "" && iss.Status == "ignored" {
			continue
		}
		items = append(items, &wikiIssueItem{
			ID: iss.ID, PageSlug: iss.PageSlug,
			IssueType: iss.IssueType, Level: string(iss.Level),
			Title: iss.Title, Description: iss.Description, Status: iss.Status,
			CreatedAt: iss.CreatedAt.Unix(),
		})
	}
	return items, nil
}

type wikiIssueItem struct {
	ID          int64  `json:"id"`
	PageSlug    string `json:"page_slug"`
	IssueType   string `json:"issue_type"`
	Level       string `json:"level"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
}

func (h *WikiHandler) FlagIssue(ctx context.Context, kbID int64, slug, issueType, description string) (int64, error) {
	issue := &domain.WikiPageIssue{
		ID: h.Snowflake.Generate(), KnowledgeBaseID: kbID,
		PageSlug: slug, IssueType: issueType,
		Level: domain.WikiIssueWarning, Title: issueType,
		Description: description, Status: "open",
	}
	if err := h.WikiRepo.CreateIssue(ctx, issue); err != nil {
		return 0, err
	}
	return issue.ID, nil
}

func (h *WikiHandler) ReadIssue(ctx context.Context, kbID int64, slug, issueID string) ([]*wikiIssueItem, error) {
	// If issueID provided, read specific issue
	if issueID != "" {
		id, err := strconv.ParseInt(issueID, 10, 64)
		if err != nil {
			return nil, err
		}
		issue, err := h.WikiRepo.GetIssue(ctx, id)
		if err != nil {
			return nil, err
		}
		return []*wikiIssueItem{{
			ID: issue.ID, PageSlug: issue.PageSlug,
			IssueType: issue.IssueType, Level: string(issue.Level),
			Title: issue.Title, Description: issue.Description, Status: issue.Status,
			CreatedAt: issue.CreatedAt.Unix(),
		}}, nil
	}
	// Otherwise list issues for KB (optionally filtered by slug)
	issues, err := h.WikiRepo.ListIssuesByKB(ctx, kbID, "")
	if err != nil {
		return nil, err
	}
	items := make([]*wikiIssueItem, 0, len(issues))
	for _, iss := range issues {
		if slug != "" && iss.PageSlug != slug {
			continue
		}
		items = append(items, &wikiIssueItem{
			ID: iss.ID, PageSlug: iss.PageSlug,
			IssueType: iss.IssueType, Level: string(iss.Level),
			Title: iss.Title, Description: iss.Description, Status: iss.Status,
			CreatedAt: iss.CreatedAt.Unix(),
		})
	}
	return items, nil
}

func (h *WikiHandler) UpdateIssue(ctx context.Context, issueID int64, status string) error {
	return h.WikiRepo.UpdateIssueStatus(ctx, issueID, status, "")
}

func (h *WikiHandler) Refresh(ctx context.Context, kbID int64) (int, error) {
	docs, _, err := h.DocRepo.ListByKB(ctx, kbID, 0, 10000, "ready")
	if err != nil {
		return 0, err
	}
	var ids []int64
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	go h.WikiPipe.Start(context.Background(), kbID, ids)
	return len(ids), nil
}

func (h *WikiHandler) RunMaintenance(ctx context.Context, kbID int64) (*pipeline.MaintenanceReport, error) {
	return h.WikiMaintain.Run(ctx, kbID)
}

// RunMaintenanceAsync fires wiki maintenance in a background goroutine and pushes
// a realtime event to the knowledge base owner when complete.
func (h *WikiHandler) RunMaintenanceAsync(ctx context.Context, kbID int64) {
	go func() {
		bgCtx := context.Background()
		logger := h.Logger.WithContext(bgCtx)
		logger.Infof("async wiki maintenance started: kb=%d", kbID)

		report, err := h.WikiMaintain.Run(bgCtx, kbID)
		if err != nil {
			logger.Errorf("async wiki maintenance failed: kb=%d err=%v", kbID, err)
			// Push failure event
			if h.Pusher != nil {
				kb, kbErr := h.KBRepo.Get(bgCtx, kbID)
				if kbErr == nil {
					h.Pusher.PushToUser(bgCtx, kb.OwnerID, event.RealtimeEvent{
						Type:    event.EventTypeWikiMaintained,
						Level:   event.EventLevelError,
						Title:   "知识库维护失败",
						Message: fmt.Sprintf("知识库「%s」维护失败：%v", kb.Name, err),
						KBID:    kbID,
					})
				}
			}
			return
		}

		// Update last_maintenance_at
		if kb, kbErr := h.KBRepo.Get(bgCtx, kbID); kbErr == nil {
			now := time.Now()
			kb.LastMaintenanceAt = &now
			_ = h.KBRepo.Update(bgCtx, kb)

			if h.Pusher != nil {
				h.Pusher.PushToUser(bgCtx, kb.OwnerID, event.RealtimeEvent{
					Type:    event.EventTypeWikiMaintained,
					Level:   event.EventLevelSuccess,
					Title:   "知识库维护完成",
					Message: fmt.Sprintf("知识库「%s」维护完成：新增 %d 页，发现 %d 个问题", kb.Name, report.PagesCreated, report.IssuesFound),
					KBID:    kbID,
					Metadata: map[string]any{
						"pages_created": report.PagesCreated,
						"issues_found":  report.IssuesFound,
						"kb_name":       kb.Name,
					},
				})
			}
		}

		logger.Infof("async wiki maintenance completed: kb=%d created=%d issues=%d",
			kbID, report.PagesCreated, report.IssuesFound)
	}()
}

func (h *WikiHandler) WikiQuery(ctx context.Context, wikiKBIDs []int64, query string, modelID int64, modelName string, history string) (*pipeline.WikiAgentOutput, error) {
	if h.WikiEinoAgent != nil {
		var ownerID int64
		var kbID int64
		if len(wikiKBIDs) > 0 {
			kbID = wikiKBIDs[0]
			if kb, err := h.KBRepo.Get(ctx, kbID); err == nil {
				ownerID = kb.OwnerID
			}
		}
		return h.WikiEinoAgent.Query(ctx, kbID, query, modelID, modelName, ownerID, history)
	}
	if h.WikiAgent != nil {
		return &pipeline.WikiAgentOutput{Answer: "Wiki Agent 正在迁移到新的工具系统。"}, nil
	}
	return &pipeline.WikiAgentOutput{Answer: "Wiki Agent 不可用"}, nil
}

func (h *WikiHandler) GetGraph(ctx context.Context, kbID int64) (*neo4j.GraphData, error) {
	logger := h.Logger.WithContext(ctx)
	data, err := h.Neo4jStore.GetGraph(ctx, kbID)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, neo4j.ErrNeo4jNotConfigured) {
		logger.Errorf("neo4j graph query failed, falling back to postgres: %v", err)
	}
	return h.buildGraphFromDB(ctx, kbID)
}

func (h *WikiHandler) buildGraphFromDB(ctx context.Context, kbID int64) (*neo4j.GraphData, error) {
	pages, err := h.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil {
		return nil, err
	}
	graph := &neo4j.GraphData{
		Nodes: make([]neo4j.GraphNode, 0, len(pages)),
		Edges: make([]neo4j.GraphEdge, 0),
	}
	nodeSet := make(map[string]bool)
	edgeCount := make(map[string]int)
	for _, page := range pages {
		if nodeSet[page.Slug] {
			continue
		}
		nodeSet[page.Slug] = true
		var inLinks []string
		if len(page.InLinks) > 0 {
			json.Unmarshal(page.InLinks, &inLinks)
		}
		graph.Nodes = append(graph.Nodes, neo4j.GraphNode{
			ID: page.Slug, Title: page.Title, PageType: string(page.PageType), Group: string(page.PageType),
			Summary: truncateContent(page.Summary, 80), CitationCount: len(inLinks),
		})
		var outLinks []string
		if len(page.OutLinks) > 0 {
			json.Unmarshal(page.OutLinks, &outLinks)
		}
		for _, target := range outLinks {
			key := page.Slug + "->" + target
			edgeCount[key]++
		}
	}
	for key, cnt := range edgeCount {
		parts := strings.SplitN(key, "->", 2)
		graph.Edges = append(graph.Edges, neo4j.GraphEdge{Source: parts[0], Target: parts[1], Weight: cnt})
	}
	return graph, nil
}
func toWikiPageRsp(page *domain.WikiPage) *wikiPageRsp {
	var aliases, outLinks, inLinks []string
	if len(page.Aliases) > 0 {
		json.Unmarshal(page.Aliases, &aliases)
	}
	if len(page.OutLinks) > 0 {
		json.Unmarshal(page.OutLinks, &outLinks)
	}
	if len(page.InLinks) > 0 {
		json.Unmarshal(page.InLinks, &inLinks)
	}
	var sourceRefs []wikiSourceRef
	if len(page.SourceRefs) > 0 {
		json.Unmarshal(page.SourceRefs, &sourceRefs)
	}
	return &wikiPageRsp{
		ID: page.ID, Slug: page.Slug, Title: page.Title,
		PageType: string(page.PageType), Content: page.Content,
		Summary: page.Summary, Version: int32(page.Version),
		CreatedAt: page.CreatedAt.Unix(), UpdatedAt: page.UpdatedAt.Unix(),
		Aliases: aliases, OutLinks: outLinks, InLinks: inLinks,
		SourceRefs: sourceRefs,
	}
}

func (h *WikiHandler) RemoveDocRefs(ctx context.Context, kbID, docID int64) {
	pages, err := h.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil {
		return
	}
	for _, page := range pages {
		if len(page.SourceRefs) == 0 {
			continue
		}
		var refs []map[string]any
		if err := json.Unmarshal(page.SourceRefs, &refs); err != nil {
			continue
		}
		found := false
		filtered := make([]map[string]any, 0, len(refs))
		for _, ref := range refs {
			id, ok := ref["doc_id"].(float64)
			if ok && int64(id) == docID {
				found = true
				continue
			}
			filtered = append(filtered, ref)
		}
		if !found {
			continue
		}
		raw, _ := json.Marshal(filtered)
		page.SourceRefs = raw
		page.Version++
		h.WikiRepo.Upsert(ctx, &page)
	}
}

func truncateContent(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
