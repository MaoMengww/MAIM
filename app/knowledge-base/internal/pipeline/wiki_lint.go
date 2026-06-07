package pipeline

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/logx"
	"github.com/maomeng/aim/pkg/snowflake"
)

type WikiLintPipeline struct {
	WikiRepo   domain.WikiPageRepo
	KBRepo     domain.KBRepo
	LLMGateway LLMGateway
	Snowflake  *snowflake.Node
	Logger     logx.Logger
}

func NewWikiLintPipeline(wikiRepo domain.WikiPageRepo, kbRepo domain.KBRepo, llmGW LLMGateway, snow *snowflake.Node, logger logx.Logger) *WikiLintPipeline {
	return &WikiLintPipeline{WikiRepo: wikiRepo, KBRepo: kbRepo, LLMGateway: llmGW, Snowflake: snow, Logger: logger}
}

func (p *WikiLintPipeline) Lint(ctx context.Context, kbID int64) ([]domain.WikiPageIssue, error) {
	pages, err := p.WikiRepo.ListAllByKB(ctx, kbID)
	if err != nil {
		return nil, err
	}
	var issues []domain.WikiPageIssue

	for _, page := range pages {
		if len([]rune(page.Content)) < 50 {
			issue, err := p.makeIssue(kbID, page.Slug,
				"low_quality", domain.WikiIssueInfo,
				"内容过短", "页面内容不足 50 字，建议更新")
			if err != nil {
				return nil, err
			}
			issues = append(issues, issue)
		}
		links := extractWikiLinks(page.Content)
		for _, link := range links {
			if _, err := p.WikiRepo.GetBySlug(ctx, kbID, link); err != nil {
				issue, err := p.makeIssue(kbID, page.Slug,
					"missing_ref", domain.WikiIssueWarning,
					"引用缺失", fmt.Sprintf("页面引用了 [[%s]] 但不存在", link))
				if err != nil {
					return nil, err
				}
				issues = append(issues, issue)
			}
		}
		if time.Since(page.UpdatedAt) > 7*24*time.Hour {
			issue, err := p.makeIssue(kbID, page.Slug,
				"stale", domain.WikiIssueWarning,
				"内容可能过期", "页面超过 7 天未更新")
			if err != nil {
				return nil, err
			}
			issues = append(issues, issue)
		}
	}
	return issues, nil
}


func (p *WikiLintPipeline) makeIssue(kbID int64, slug, issueType string, level domain.WikiIssueLevel, title, desc string) (domain.WikiPageIssue, error) {
	id, err := p.Snowflake.Generate()
	if err != nil {
		return domain.WikiPageIssue{}, fmt.Errorf("generate issue id failed: %w", err)
	}
	return domain.WikiPageIssue{
		ID: id, KnowledgeBaseID: kbID,
		PageSlug: slug, IssueType: issueType, Level: level,
		Title: title, Description: desc, Status: "open",
	}, nil
}

var linkRegex = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

func extractWikiLinks(content string) []string {
	matches := linkRegex.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var links []string
	for _, m := range matches {
		if !seen[m[1]] {
			seen[m[1]] = true
			links = append(links, m[1])
		}
	}
	return links
}
