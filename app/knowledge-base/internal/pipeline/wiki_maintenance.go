package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	llmgatewaypb "github.com/maomeng/aim/app/llm-gateway/pb/llmgateway"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/app/knowledge-base/internal/eventpush"
	neo4j "github.com/maomeng/aim/app/knowledge-base/internal/infra/neo4j"
	"github.com/maomeng/aim/pkg/event"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
)

type MaintenanceReport struct {
	KBID          int64
	StartedAt     time.Time
	CompletedAt   time.Time
	PagesCreated  int
	CreatedTitles []string
	IssuesFound   int
	Issues        []domain.WikiPageIssue
	Duration      time.Duration
}

// WikiMaintenanceAgent orchestrates maintenance: ingest new docs, then run the ReAct agent.
type WikiMaintenanceAgent struct {
	WikiRepo   domain.WikiPageRepo
	DocRepo    domain.DocumentRepo
	KBRepo     domain.KBRepo
	FileStore  domain.FileStore
	LLMGateway LLMGateway
	Snowflake  *snowflake.Node
	Logger     logx.Logger
	Neo4jStore neo4j.GraphStore
	LogWriter  *LogWriter
	ReActAgent *MaintenanceReActAgent
	Pusher     eventpush.Pusher

	mu         sync.Mutex
	runningKBs map[int64]bool
}

func NewWikiMaintenanceAgent(
	graphStore neo4j.GraphStore,
	wikiRepo domain.WikiPageRepo, docRepo domain.DocumentRepo,
	kbRepo domain.KBRepo, fileStore domain.FileStore,
	llmGW LLMGateway, snow *snowflake.Node, logger logx.Logger,
) *WikiMaintenanceAgent {
	return &WikiMaintenanceAgent{
		WikiRepo: wikiRepo, DocRepo: docRepo, KBRepo: kbRepo,
		FileStore: fileStore, LLMGateway: llmGW,
		Snowflake: snow, Logger: logger,
		Neo4jStore: graphStore,
		runningKBs: make(map[int64]bool),
	}
}

// WithPusher sets the realtime event pusher for maintenance completion notifications.
func (a *WikiMaintenanceAgent) WithPusher(pusher eventpush.Pusher) *WikiMaintenanceAgent {
	a.Pusher = pusher
	return a
}

func (a *WikiMaintenanceAgent) Run(ctx context.Context, kbID int64) (*MaintenanceReport, error) {
	a.mu.Lock()
	if a.runningKBs[kbID] {
		a.mu.Unlock()
		return nil, fmt.Errorf("knowledge base %d maintenance is already running", kbID)
	}
	a.runningKBs[kbID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.runningKBs, kbID)
		a.mu.Unlock()
	}()

	report := &MaintenanceReport{KBID: kbID, StartedAt: time.Now()}
	logger := a.Logger.WithContext(ctx)
	logger.Infof("wiki maintenance start: kb=%d", kbID)

	// Step 1: ingest docs that don't have a wiki page yet
	docs, _, err := a.DocRepo.ListByKB(ctx, kbID, 0, 10000, "ready")
	if err == nil {
		var newDocIDs []int64
		var newTitles []string
		for _, d := range docs {
			slug := "entity/" + slugify(d.Title)
			if _, err := a.WikiRepo.GetBySlug(ctx, kbID, slug); err != nil {
				newDocIDs = append(newDocIDs, d.ID)
				newTitles = append(newTitles, d.Title)
			}
		}
		if len(newDocIDs) > 0 {
			logger.Infof("maintenance: %d new docs for kb %d", len(newDocIDs), kbID)
			pipe := &WikiIngestPipeline{
				WikiRepo: a.WikiRepo, FileStore: a.FileStore,
				DocRepo: a.DocRepo, KBRepo: a.KBRepo,
				LLMGateway: a.LLMGateway, Snowflake: a.Snowflake, Logger: a.Logger,
				Neo4jStore: a.Neo4jStore,
			}
			pipe.Start(ctx, kbID, newDocIDs)
			report.PagesCreated = len(newDocIDs)
			report.CreatedTitles = newTitles
		}
	}

	// Step 2: run ReAct agent for intelligent maintenance
	logger.Infof("maintenance: running react agent for kb %d", kbID)
	if a.ReActAgent != nil {
		agentReport, agentErr := a.ReActAgent.Run(ctx, kbID)
		if agentErr != nil {
			logger.Errorf("react maintenance agent failed: %v", agentErr)
			// Merge partial results from agentReport before returning error
			if agentReport != nil {
				report.IssuesFound += agentReport.IssuesFound
				report.Issues = append(report.Issues, agentReport.Issues...)
			}
			report.CompletedAt = time.Now()
			report.Duration = report.CompletedAt.Sub(report.StartedAt)
			return report, agentErr
		}
		report.IssuesFound += agentReport.IssuesFound
		report.Issues = append(report.Issues, agentReport.Issues...)
	}

	// Step 3: update index page
	pipe := &WikiIngestPipeline{
		WikiRepo: a.WikiRepo, FileStore: a.FileStore,
		DocRepo: a.DocRepo, KBRepo: a.KBRepo,
		LLMGateway: a.LLMGateway, Snowflake: a.Snowflake, Logger: a.Logger,
		Neo4jStore: a.Neo4jStore,
	}
	pipe.updateIndexPage(ctx, kbID)

	report.CompletedAt = time.Now()
	report.Duration = report.CompletedAt.Sub(report.StartedAt)

	logger.Infof("wiki maintenance done: kb=%d created=%d issues=%d duration=%s",
		kbID, report.PagesCreated, report.IssuesFound, report.Duration)
	return report, nil
}

// PushResult updates last_maintenance_at and pushes a realtime event to the KB owner.
// The prefix parameter distinguishes scheduled ("自动") from manual ("") maintenance in messages.
func (a *WikiMaintenanceAgent) PushResult(ctx context.Context, kbID int64, report *MaintenanceReport, runErr error, prefix string) {
	logger := a.Logger.WithContext(ctx)

	if runErr != nil {
		logger.Errorf("wiki maintenance failed: kb=%d err=%v", kbID, runErr)
		if a.Pusher != nil {
			kb, kbErr := a.KBRepo.Get(ctx, kbID)
			if kbErr == nil {
				_ = a.Pusher.PushToUser(ctx, kb.OwnerID, event.RealtimeEvent{
					Type:    event.EventTypeWikiMaintained,
					Level:   event.EventLevelError,
					Title:   "知识库维护失败",
					Message: fmt.Sprintf("知识库「%s」%s维护失败：%v", kb.Name, prefix, runErr),
					KBID:    kbID,
				})
			}
		}
		return
	}

	// Update last_maintenance_at
	kb, kbErr := a.KBRepo.Get(ctx, kbID)
	if kbErr != nil {
		logger.Errorf("get kb for push result failed: kb=%d err=%v", kbID, kbErr)
		return
	}
	now := time.Now()
	kb.LastMaintenanceAt = &now
	_ = a.KBRepo.Update(ctx, kb)

	if a.Pusher != nil {
		_ = a.Pusher.PushToUser(ctx, kb.OwnerID, event.RealtimeEvent{
			Type:    event.EventTypeWikiMaintained,
			Level:   event.EventLevelSuccess,
			Title:   "知识库维护完成",
			Message: fmt.Sprintf("知识库「%s」%s维护完成：新增 %d 页，发现 %d 个问题", kb.Name, prefix, report.PagesCreated, report.IssuesFound),
			KBID:    kbID,
			Metadata: map[string]any{
				"pages_created": report.PagesCreated,
				"issues_found":  report.IssuesFound,
				"kb_name":       kb.Name,
			},
		})
	}

	logger.Infof("wiki maintenance completed: kb=%d created=%d issues=%d",
		kbID, report.PagesCreated, report.IssuesFound)
}

// =============================================================================
// MaintenanceReActAgent — autonomous ReAct agent for wiki maintenance
// =============================================================================

type MaintenanceReActAgent struct {
	deps      *WikiToolDeps
	llmClient llmgatewaypb.LLMGatewayClient
	snowflake *snowflake.Node
	LogWriter *LogWriter
	logger    logx.Logger
}

func NewMaintenanceReActAgent(
	wikiRepo domain.WikiPageRepo,
	docRepo domain.DocumentRepo,
	kbRepo domain.KBRepo,
	fileStore domain.FileStore,
	llmClient llmgatewaypb.LLMGatewayClient,
	snow *snowflake.Node,
	logger logx.Logger,
) *MaintenanceReActAgent {
	deps := &WikiToolDeps{
		WikiRepo:  wikiRepo,
		DocRepo:   docRepo,
		KBRepo:    kbRepo,
		FileStore: fileStore,
		Snowflake: snow,
		Logger:    logger,
	}
	return &MaintenanceReActAgent{
		deps:      deps,
		llmClient: llmClient,
		snowflake: snow,
		logger:    logger,
	}
}

func (a *MaintenanceReActAgent) Run(ctx context.Context, kbID int64) (*MaintenanceReport, error) {
	report := &MaintenanceReport{KBID: kbID, StartedAt: time.Now()}

	kb, err := a.deps.KBRepo.Get(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get kb %d: %w", kbID, err)
	}
	modelID := kb.PipelineConfig.Wiki.ModelID
	if modelID == 0 {
		return nil, fmt.Errorf("kb %d has no model_id configured for wiki maintenance", kbID)
	}

	systemPrompt := a.buildSystemPrompt()

	tools := NewWikiTools(a.deps)
	chatModel := newEinoChatModel(a.llmClient, modelID, kb.OwnerID)

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MessageModifier: func(ctx context.Context, msgs []*schema.Message) []*schema.Message {
			result := make([]*schema.Message, 0, len(msgs)+1)
			result = append(result, &schema.Message{
				Role:    schema.System,
				Content: systemPrompt,
			})
			result = append(result, msgs...)
			return result
		},
		MaxStep: 50,
	})
	if err != nil {
		return nil, fmt.Errorf("create maintenance agent: %w", err)
	}

	userPrompt := fmt.Sprintf(
		"Begin scheduled maintenance for knowledge base \"%s\" (ID: %d). "+
			"Start by reading the wiki index to understand the current structure, then assess what needs to be done.",
		kb.Name, kbID,
	)

	msg, agentErr := agent.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: userPrompt},
	})

	// Always generate log and issues, even if agent hit max steps
	report.CompletedAt = time.Now()
	report.Duration = report.CompletedAt.Sub(report.StartedAt)
	report.Issues = a.collectOpenIssues(ctx, kbID)
	report.IssuesFound = len(report.Issues)

	if agentErr != nil {
		a.logger.WithContext(ctx).Errorf("maintenance agent run: kb=%d err=%v", kbID, agentErr)
	} else {
		a.logger.WithContext(ctx).Infof("wiki maintenance react agent done: kb=%d duration=%s",
			kbID, report.Duration)
	}

	if msg != nil && msg.Content != "" && a.LogWriter != nil {
		logAction := "自动维护"
		if agentErr != nil {
			logAction = "自动维护（异常结束）"
		}
		_ = a.LogWriter.Write(ctx, kbID, logAction, msg.Content)
	}

	if agentErr != nil {
		return report, agentErr
	}
	return report, nil
}

func (a *MaintenanceReActAgent) buildSystemPrompt() string {
	return `You are an autonomous wiki maintenance agent. Your task is to review and maintain a wiki knowledge base by examining pages, detecting issues, and fixing them.

## CORE RULES — YOU MUST FOLLOW THESE

1. **NEVER fabricate information.** Every fact you write into a wiki page must be traceable to a source document. If you don't have a source to verify a claim, do NOT write it. Instead, flag an issue.

2. **Stay faithful to sources.** When rewriting or updating a page, only use information from the page's referenced source documents. Do not add interpretation, speculation, or external knowledge.

3. **Minimal changes.** Update only what needs updating. Prefer wiki_replace_text for targeted edits over wiki_write_page for full rewrites.

4. **Fix or flag.** If you can fix an issue directly, do so. If you cannot (e.g. insufficient source documents, ambiguous contradiction), use wiki_flag_issue with a clear description.

## WIKI LINK SYNTAX — CRITICAL

When creating wiki links, use EXACTLY this format: [[slug]] with the slug directly inside double brackets.
- CORRECT: [[concept/leader-election]]
- CORRECT: [[entity/raft]]
- WRONG: [[concept/[[concept/leader-election]]] — NEVER nest brackets
- WRONG: [[concept/Leader（领导者）]] — slug must NOT contain Chinese characters or special chars

A slug follows the pattern: type/english-name where type is entity/concept/summary/synthesis/comparison.
The text inside [[ ... ]] must be a VALID SLUG, never a title or description.

## ISSUE TYPES

Use wiki_flag_issue with the appropriate type:
- factual_error — Page contains claims contradicted by source documents
- merge_conflict — Two pages should be merged but need human review
- outdated — Page content is stale relative to source documents
- incomplete — Page is missing important information that should be covered
- duplicate — Two or more pages cover the same topic
- broken_link — A [[slug]] link points to a non-existent page (use only after searching and finding no replacement)
- needs_review — Any other situation requiring human judgment

## PRIORITY MAINTENANCE TASKS

Review these areas in this order:

1. **Synthesis & Comparison pages** — Check if newer source documents contain information that should be merged into existing synthesis or comparison pages. Read existing pages, then check source documents via wiki_read_source_doc, then update or flag.

2. **Contradictions** — Compare wiki page claims with source documents. If a page makes a claim contradicted by its source, flag it.

3. **Broken links** — Find [[slug]] references pointing to non-existent pages. Search for the correct target by title or content, then fix with wiki_replace_text. Only flag if no target can be found.

4. **Duplicate & similar pages** — Detect pages with substantially overlapping content or very similar titles (e.g. "Leader" and "Leader（领导者）"). Merge duplicates by consolidating content into one page and redirecting the other via wiki_rename_page. Flag if unsure.

5. **Low quality pages** — Pages with shallow content, missing key information, or unclear structure. Read the source documents and improve the page. If no source documents exist for a page, flag it.

## FINAL SUMMARY — CRITICAL

Your very last message MUST be a structured maintenance summary written in Chinese. This summary will be captured as a log entry in the wiki's log page, so it must be comprehensive and well-organized. Budget your steps to ensure you always have time to produce this summary.

You MUST end your final response with a "## 维护总结" section containing:

### 维护总结
- **Reviewed pages**: List all page slugs you examined
- **Changes made**: List pages created/updated with slug and reason
- **Issues flagged**: List issues (type, page slug, description)
- **Duplicates handled**: Pages merged or flagged as duplicate
- **Unresolved**: What you chose not to change and why

## WORKFLOW

1. Start by reading the index to understand the wiki structure.
2. Use wiki_list_pages to filter pages by type (especially synthesis and comparison).
3. Check for duplicate/similar pages by comparing titles.
4. Read pages and their source documents to assess quality and completeness.
5. Fix what you can. Flag what you can't.
6. End with a structured ## 维护总结 (see FINAL SUMMARY above).`
}

func (a *MaintenanceReActAgent) collectOpenIssues(ctx context.Context, kbID int64) []domain.WikiPageIssue {
	issues, err := a.deps.WikiRepo.ListIssuesByKB(ctx, kbID, "")
	if err != nil {
		return nil
	}
	var open []domain.WikiPageIssue
	for _, issue := range issues {
		if issue.Status == "open" {
			open = append(open, issue)
		}
	}
	return open
}
