package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
)

// WikiToolDeps contains shared dependencies for all wiki tools.
type WikiToolDeps struct {
	WikiRepo  domain.WikiPageRepo
	KBRepo    domain.KBRepo
	DocRepo   domain.DocumentRepo
	FileStore domain.FileStore
	Snowflake *snowflake.Node
	Logger    logx.Logger
}

// wikiTool implements tool.InvokableTool with a function callback.
type wikiTool struct {
	deps  *WikiToolDeps
	info  *schema.ToolInfo
	runFn func(ctx context.Context, args map[string]any) (string, error)
}

func (t *wikiTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *wikiTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	return t.runFn(ctx, args)
}

// NewWikiTools creates all wiki tools.
func NewWikiTools(deps *WikiToolDeps) []tool.BaseTool {
	return []tool.BaseTool{
		newWikiReadIndexTool(deps),
		newWikiReadPageTool(deps),
		newWikiSearchTool(deps),
		newWikiReadSourceDocTool(deps),
		newWikiWritePageTool(deps),
		newWikiReplaceTextTool(deps),
		newWikiRenamePageTool(deps),
		newWikiDeletePageTool(deps),
		newWikiFlagIssueTool(deps),
		newWikiReadIssueTool(deps),
		newWikiUpdateIssueTool(deps),
		newWikiListPagesTool(deps),
	}
}

// --- argument helpers (JSON unmarshals numbers as float64) ---

func getInt64(args map[string]any, key string) int64 {
	v, ok := args[key]
	if !ok {
		return 0
	}
	f, _ := v.(float64)
	return int64(f)
}

func getString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func getStringSlice(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// --- tool constructors ---

func newWikiReadIndexTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_read_index",
			Desc: "Read the wiki index directory of the knowledge base, listing all page slugs and titles",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (optional, returns a hint if empty)"},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			kbID := getInt64(args, "knowledge_base_id")
			if kbID == 0 {
				return "Please provide the knowledge_base_id parameter to specify the knowledge base.", nil
			}
			page, err := deps.WikiRepo.GetBySlug(ctx, kbID, "index")
			if err != nil {
				return "The knowledge base does not have a wiki index yet.", nil
			}
			return fmt.Sprintf("# Knowledge Base Index\n\n%s", page.Content), nil
		},
	}
}

func newWikiReadPageTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_read_page",
			Desc: "Read the full content of a wiki page",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"slug":              {Type: schema.String, Desc: "Page slug (required)", Required: true},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (optional)"},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			slug := getString(args, "slug")
			kbID := getInt64(args, "knowledge_base_id")
			if slug == "" {
				return "The slug parameter is required.", nil
			}
			if kbID == 0 {
				return "Please provide the knowledge_base_id parameter.", nil
			}
			page, err := deps.WikiRepo.GetBySlug(ctx, kbID, slug)
			if err != nil {
				return fmt.Sprintf("页面 [[%s]] 不存在。", slug), nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("# %s (type: %s)\n\n%s", page.Title, page.PageType, page.Content))
			if len(page.OutLinks) > 0 {
				sb.WriteString(fmt.Sprintf("\n\nReferences: %s", string(page.OutLinks)))
			}
			if len(page.InLinks) > 0 {
				sb.WriteString(fmt.Sprintf("\nCited by: %s", string(page.InLinks)))
			}
			return sb.String(), nil
		},
	}
}

func newWikiSearchTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_search",
			Desc: "Search wiki page titles and content using POSIX regex",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"queries":           {Type: schema.Array, Desc: "Search keyword list (required)", Required: true, ElemInfo: &schema.ParameterInfo{Type: schema.String}},
				"limit":             {Type: schema.Integer, Desc: "Maximum number of results (default 10)"},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (optional, searches all KBs when empty)"},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			queries := getStringSlice(args, "queries")
			limit := int(getInt64(args, "limit"))
			kbID := getInt64(args, "knowledge_base_id")
			if len(queries) == 0 {
				return "The queries parameter is required.", nil
			}
			if limit <= 0 {
				limit = 10
			}

			seen := make(map[string]bool)
			var results []domain.WikiPage

			for _, q := range queries {
				pages, err := deps.WikiRepo.RegexSearch(ctx, kbID, q, limit)
				if err != nil {
					continue
				}
				for _, p := range pages {
					if !seen[p.Slug] {
						seen[p.Slug] = true
						results = append(results, p)
					}
				}
				if len(results) >= limit {
					break
				}
			}

			if len(results) > limit {
				results = results[:limit]
			}
			if len(results) == 0 {
				return "No matching wiki pages found.", nil
			}

			var sb strings.Builder
			for _, p := range results {
				sb.WriteString(fmt.Sprintf("- [[%s]] %s: %s\n", p.Slug, p.Title, truncate(p.Content, 150)))
			}
			return sb.String(), nil
		},
	}
}

func newWikiReadSourceDocTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_read_source_doc",
			Desc: "Read the original content of a source document",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"doc_id": {Type: schema.Integer, Desc: "Document ID (required)", Required: true},
				"query":  {Type: schema.String, Desc: "Optional regex to filter matching lines from document content"},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			docID := getInt64(args, "doc_id")
			query := getString(args, "query")
			if docID == 0 {
				return "The doc_id parameter is required.", nil
			}

			doc, err := deps.DocRepo.Get(ctx, docID)
			if err != nil {
				return fmt.Sprintf("Document %d does not exist.", docID), nil
			}
			if doc.MinioKey == "" {
				return "Document has not been uploaded or content is empty.", nil
			}

			reader, err := deps.FileStore.Get(ctx, doc.MinioKey)
			if err != nil {
				return fmt.Sprintf("Failed to read document content: %v", err), nil
			}
			defer reader.Close()

			buf := new(bytes.Buffer)
			if _, err := io.Copy(buf, reader); err != nil {
				return fmt.Sprintf("Failed to read document content: %v", err), nil
			}
			content := buf.String()

			if query != "" {
				lines := strings.Split(content, "\n")
				var matched []string
				for _, line := range lines {
					if matchedLine, err := regexp.MatchString(query, line); err == nil && matchedLine {
						matched = append(matched, line)
					}
				}
				content = strings.Join(matched, "\n")
			}

			content = truncate(content, 4000)
			return content, nil
		},
	}
}

func newWikiWritePageTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_write_page",
			Desc: "Create or overwrite a wiki page",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"slug":              {Type: schema.String, Desc: "Page slug (required)", Required: true},
				"title":             {Type: schema.String, Desc: "Page title (required)", Required: true},
				"content":           {Type: schema.String, Desc: "Page content (required)", Required: true},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (required)", Required: true},
				"page_type":         {Type: schema.String, Desc: "页面类型（必填）：entity/concept/summary/synthesis/comparison", Required: true, Enum: []string{"entity", "concept", "summary", "synthesis", "comparison"}},
				"aliases":           {Type: schema.Array, Desc: "Alias list (optional)", ElemInfo: &schema.ParameterInfo{Type: schema.String}},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			slug := getString(args, "slug")
			title := getString(args, "title")
			content := getString(args, "content")
			kbID := getInt64(args, "knowledge_base_id")
			pageType := getString(args, "page_type")
			aliases := getStringSlice(args, "aliases")

			if slug == "" || title == "" || content == "" || kbID == 0 || pageType == "" {
				return "Incomplete parameters: slug, title, content, knowledge_base_id, and page_type are required.", nil
			}

			pageID, err := deps.Snowflake.Generate()
				if err != nil {
					return "", fmt.Errorf("generate page id failed: %w", err)
				}
				page := &domain.WikiPage{
				ID:              pageID,
				KnowledgeBaseID: kbID,
				Slug:            slug,
				Title:           title,
				PageType:        domain.WikiPageType(pageType),
				Status:          domain.WikiPagePublished,
				Content:         content,
			}
			if len(aliases) > 0 {
				page.Aliases = toRawJSON(aliases)
			}

			// Preserve source refs when updating an existing page
			if existing, err := deps.WikiRepo.GetBySlug(ctx, kbID, slug); err == nil {
				if len(existing.SourceRefs) > 0 {
					page.SourceRefs = existing.SourceRefs
				}
				if len(page.Aliases) == 0 && len(existing.Aliases) > 0 {
					page.Aliases = existing.Aliases
				}
			}

			if err := deps.WikiRepo.Upsert(ctx, page); err != nil {
				return fmt.Sprintf("Write failed: %v", err), nil
			}
			return fmt.Sprintf("页面 [[%s]] 已创建/更新。", slug), nil
		},
	}
}

func newWikiReplaceTextTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_replace_text",
			Desc: "Replace exact text in a wiki page",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"slug":              {Type: schema.String, Desc: "Page slug (required)", Required: true},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (required)", Required: true},
				"old_text":          {Type: schema.String, Desc: "Old text to replace (required)", Required: true},
				"new_text":          {Type: schema.String, Desc: "New replacement text (required)", Required: true},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			slug := getString(args, "slug")
			kbID := getInt64(args, "knowledge_base_id")
			oldText := getString(args, "old_text")
			newText := getString(args, "new_text")
			if slug == "" || kbID == 0 || oldText == "" || newText == "" {
				return "Incomplete parameters: slug, knowledge_base_id, old_text, and new_text are required.", nil
			}

			page, err := deps.WikiRepo.GetBySlug(ctx, kbID, slug)
			if err != nil {
				return fmt.Sprintf("页面 [[%s]] 不存在。", slug), nil
			}

			if !strings.Contains(page.Content, oldText) {
				return fmt.Sprintf("页面 [[%s]] 中未找到匹配文本。", slug), nil
			}

			page.Content = strings.ReplaceAll(page.Content, oldText, newText)
			page.Version++
			if err := deps.WikiRepo.Upsert(ctx, page); err != nil {
				return fmt.Sprintf("Replace failed: %v", err), nil
			}
			return fmt.Sprintf("页面 [[%s]] 中的文本已替换。", slug), nil
		},
	}
}

func newWikiRenamePageTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_rename_page",
			Desc: "Rename a wiki page slug and cascade update all cross-page reference links",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"slug":              {Type: schema.String, Desc: "Current slug (required)", Required: true},
				"new_slug":          {Type: schema.String, Desc: "New slug (required)", Required: true},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (required)", Required: true},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			slug := getString(args, "slug")
			newSlug := getString(args, "new_slug")
			kbID := getInt64(args, "knowledge_base_id")
			if slug == "" || newSlug == "" || kbID == 0 {
				return "Incomplete parameters: slug, new_slug, and knowledge_base_id are required.", nil
			}
			if slug == newSlug {
				return "Old and new slugs are the same, no rename needed.", nil
			}

			// Check old slug exists
			page, err := deps.WikiRepo.GetBySlug(ctx, kbID, slug)
			if err != nil {
				return fmt.Sprintf("页面 [[%s]] 不存在。", slug), nil
			}

			// Check new slug doesn't conflict
			existing, err := deps.WikiRepo.GetBySlug(ctx, kbID, newSlug)
			if err == nil && existing != nil {
				return fmt.Sprintf("新 slug [[%s]] 已被占用。", newSlug), nil
			}

			// Rename: soft-delete old record, create new record with new slug
			oldID := page.ID
			page.Slug = newSlug
			newID, err := deps.Snowflake.Generate()
			if err != nil {
				return "", fmt.Errorf("generate page id failed: %w", err)
			}
			page.ID = newID
			page.Version++

			if err := deps.WikiRepo.SoftDelete(ctx, kbID, slug); err != nil {
				return fmt.Sprintf("Failed to delete old page: %v", err), nil
			}
			if err := deps.WikiRepo.Upsert(ctx, page); err != nil {
				return fmt.Sprintf("Failed to create new page: %v", err), nil
			}

			// Cascade: update [[old_slug]] -> [[new_slug]] in all other pages
			allPages, listErr := deps.WikiRepo.ListAllByKB(ctx, kbID)
			if listErr == nil {
				oldLink := "[[" + slug + "]]"
				newLink := "[[" + newSlug + "]]"
				for _, p := range allPages {
					if p.Slug == newSlug || p.ID == oldID {
						continue
					}
					if strings.Contains(p.Content, oldLink) {
						p.Content = strings.ReplaceAll(p.Content, oldLink, newLink)
						p.Version++
						_ = deps.WikiRepo.Upsert(ctx, &p)
					}
				}
			}

			return fmt.Sprintf("页面 [[%s]] 已重命名为 [[%s]]，并更新了所有引用链接。", slug, newSlug), nil
		},
	}
}

func newWikiDeletePageTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_delete_page",
			Desc: "Delete a wiki page and clean up dead links in other pages",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"slug":              {Type: schema.String, Desc: "Page slug (required)", Required: true},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (required)", Required: true},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			slug := getString(args, "slug")
			kbID := getInt64(args, "knowledge_base_id")
			if slug == "" || kbID == 0 {
				return "Incomplete parameters: slug and knowledge_base_id are required.", nil
			}

			if err := deps.WikiRepo.SoftDelete(ctx, kbID, slug); err != nil {
				return fmt.Sprintf("Delete failed: %v", err), nil
			}

			// Cleanup dead links in other pages
			allPages, listErr := deps.WikiRepo.ListAllByKB(ctx, kbID)
			if listErr == nil {
				oldLink := "[[" + slug + "]]"
				replacement := "~~" + slug + "~~(deleted)"
				for _, p := range allPages {
					if strings.Contains(p.Content, oldLink) {
						p.Content = strings.ReplaceAll(p.Content, oldLink, replacement)
						p.Version++
						_ = deps.WikiRepo.Upsert(ctx, &p)
					}
				}
			}

			return fmt.Sprintf("页面 [[%s]] 已删除，并清理了死链。", slug), nil
		},
	}
}

func newWikiFlagIssueTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_flag_issue",
			Desc: "Flag issues on a wiki page (factual error, merge conflict, outdated)",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"slug":              {Type: schema.String, Desc: "Page slug (required)", Required: true},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (required)", Required: true},
				"issue_type":        {Type: schema.String, Desc: "Issue type (required): factual_error/merge_conflict/outdated/incomplete/duplicate/broken_link/needs_review", Required: true, Enum: []string{"factual_error", "merge_conflict", "outdated", "incomplete", "duplicate", "broken_link", "needs_review"}},
				"description":       {Type: schema.String, Desc: "Issue description (required)", Required: true},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			slug := getString(args, "slug")
			kbID := getInt64(args, "knowledge_base_id")
			issueType := getString(args, "issue_type")
			description := getString(args, "description")
			if slug == "" || kbID == 0 || issueType == "" || description == "" {
				return "Incomplete parameters: slug, knowledge_base_id, issue_type, and description are required.", nil
			}

			title := issueTypeLabel(issueType) + ": " + slug
			issueID, err := deps.Snowflake.Generate()
				if err != nil {
					return "", fmt.Errorf("generate issue id failed: %w", err)
				}
				issue := &domain.WikiPageIssue{
				ID:              issueID,
				KnowledgeBaseID: kbID,
				PageSlug:        slug,
				IssueType:       issueType,
				Level:           domain.WikiIssueWarning,
				Title:           title,
				Description:     description,
				Status:          "open",
			}

			if err := deps.WikiRepo.CreateIssue(ctx, issue); err != nil {
				return fmt.Sprintf("Failed to flag issue: %v", err), nil
			}
			return fmt.Sprintf("已为页面 [[%s]] 标记问题: %s。", slug, issueType), nil
		},
	}
}

func newWikiReadIssueTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_read_issue",
			Desc: "View wiki issue list or specific issue details",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"slug":              {Type: schema.String, Desc: "Page slug (optional, filter by page)"},
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (optional)"},
				"issue_id":          {Type: schema.Integer, Desc: "Issue ID (optional, view specific issue)"},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			slug := getString(args, "slug")
			kbID := getInt64(args, "knowledge_base_id")
			issueID := getInt64(args, "issue_id")

			if issueID > 0 {
				issue, err := deps.WikiRepo.GetIssue(ctx, issueID)
				if err != nil {
					return fmt.Sprintf("Issue %d does not exist.", issueID), nil
				}
				return formatIssue(issue), nil
			}

			if kbID == 0 {
				return "Please provide the knowledge_base_id parameter to view the issue list.", nil
			}

			issues, err := deps.WikiRepo.ListIssuesByKB(ctx, kbID, "")
			if err != nil {
				return "Failed to retrieve issue list.", nil
			}

			if slug != "" {
				var filtered []domain.WikiPageIssue
				for _, issue := range issues {
					if issue.PageSlug == slug {
						filtered = append(filtered, issue)
					}
				}
				issues = filtered
			}

			if len(issues) == 0 {
				return "No issues found.", nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Total %d issues:\n\n", len(issues)))
			for _, issue := range issues {
				sb.WriteString(fmt.Sprintf("- [#%d] [%s] %s (状态: %s)\n", issue.ID, issue.IssueType, issue.Title, issue.Status))
			}
			return sb.String(), nil
		},
	}
}

func newWikiUpdateIssueTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_update_issue",
			Desc: "Update wiki issue status",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"issue_id": {Type: schema.Integer, Desc: "Issue ID (required)", Required: true},
				"status":   {Type: schema.String, Desc: "Status (required): resolved/ignored", Required: true, Enum: []string{"resolved", "ignored"}},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			issueID := getInt64(args, "issue_id")
			status := getString(args, "status")
			if issueID == 0 || status == "" {
				return "Incomplete parameters: issue_id and status are required.", nil
			}

			if err := deps.WikiRepo.UpdateIssueStatus(ctx, issueID, status, ""); err != nil {
				return fmt.Sprintf("Failed to update issue status: %v", err), nil
			}
			return fmt.Sprintf("Issue #%d status has been updated to %s.", issueID, status), nil
		},
	}
}

// --- helpers ---

func issueTypeLabel(t string) string {
	switch t {
	case "factual_error":
		return "事实错误"
	case "merge_conflict":
		return "合并冲突"
	case "outdated":
		return "内容过时"
	case "incomplete":
		return "内容不完整"
	case "duplicate":
		return "内容重复"
	case "broken_link":
		return "链接失效"
	case "needs_review":
		return "需人工审核"
	default:
		return t
	}
}

func newWikiListPagesTool(deps *WikiToolDeps) *wikiTool {
	return &wikiTool{
		deps: deps,
		info: &schema.ToolInfo{
			Name: "wiki_list_pages",
			Desc: "List all wiki pages, optionally filtered by page type (entity/concept/synthesis/comparison/summary/index/log)",
			ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
				"knowledge_base_id": {Type: schema.Integer, Desc: "Knowledge base ID (required)", Required: true},
				"page_type":         {Type: schema.String, Desc: "Optional filter: entity/concept/synthesis/comparison/summary"},
			}),
		},
		runFn: func(ctx context.Context, args map[string]any) (string, error) {
			kbID := getInt64(args, "knowledge_base_id")
			pageType := getString(args, "page_type")
			if kbID == 0 {
				return "Please provide knowledge_base_id.", nil
			}

			var pages []domain.WikiPage
			var err error
			if pageType != "" {
				pages, err = deps.WikiRepo.ListByKBAndType(ctx, kbID, domain.WikiPageType(pageType))
			} else {
				pages, err = deps.WikiRepo.ListAllByKB(ctx, kbID)
			}
			if err != nil {
				return fmt.Sprintf("Failed to list pages: %v", err), nil
			}
			if len(pages) == 0 {
				return "No pages found.", nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Total %d pages:\n\n", len(pages)))
			for _, p := range pages {
				sb.WriteString(fmt.Sprintf("- [[%s]] | %s | type: %s | updated: %s\n",
					p.Slug, p.Title, p.PageType, p.UpdatedAt.Format("2006-01-02")))
			}
			return sb.String(), nil
		},
	}
}

// =============================================================================
// Backward-compatible stubs for WikiReActAgent / WikiAgentInput / WikiAgentOutput
// The handler and service context still reference these types. They will be
// removed in a follow-up task that migrates to the new tool-based system.
// =============================================================================

type WikiAgentInput struct {
	WikiKBIDs []int64 `json:"wiki_kb_ids"`
	Query     string  `json:"query"`
	ModelId   int64   `json:"model_id"`
	ModelName string  `json:"model_name"`
	History   string  `json:"history,omitempty"`
	OwnerID   int64   `json:"owner_id"`
}

type WikiAgentOutput struct {
	Answer       string   `json:"answer"`
	References   []string `json:"references"`
	UpdatedSlugs []string `json:"updated_slugs,omitempty"`
}

// WikiReActAgent is a legacy stub that will be replaced in a follow-up task.
// It is kept so that handler/wiki_handler.go and svc/servicecontext.go
// continue to compile during the migration to the new tool-based system.
type WikiReActAgent struct{}

func NewWikiReActAgent(_ domain.WikiPageRepo, _ domain.KBRepo, _ LLMGateway, _ *snowflake.Node, _ logx.Logger) *WikiReActAgent {
	return &WikiReActAgent{}
}

func (a *WikiReActAgent) Query(_ context.Context, _ *WikiAgentInput) (*WikiAgentOutput, error) {
	return &WikiAgentOutput{Answer: "Wiki Agent is being migrated to the new tool system."}, nil
}

func (a *WikiReActAgent) RebuildIndex(_ context.Context, _ int64) {}

func formatIssue(issue *domain.WikiPageIssue) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Issue #%d\n\n", issue.ID))
	sb.WriteString(fmt.Sprintf("- 页面: [[%s]]\n", issue.PageSlug))
	sb.WriteString(fmt.Sprintf("- Type: %s\n", issueTypeLabel(issue.IssueType)))
	sb.WriteString(fmt.Sprintf("- Status: %s\n", issue.Status))
	sb.WriteString(fmt.Sprintf("- Level: %s\n", issue.Level))
	sb.WriteString(fmt.Sprintf("- Title: %s\n", issue.Title))
	sb.WriteString(fmt.Sprintf("- Description: %s\n", issue.Description))
	sb.WriteString(fmt.Sprintf("- Created: %s\n", issue.CreatedAt.Format("2006-01-02 15:04:05")))
	if issue.ResolvedAt != nil {
		sb.WriteString(fmt.Sprintf("- Resolved: %s\n", issue.ResolvedAt.Format("2006-01-02 15:04:05")))
	}
	return sb.String()
}
