package domain

import "context"

// WikiPageLite is a lightweight projection of a wiki page used by
// FindSimilarPages — avoids loading the full content/text fields.
type WikiPageLite struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	PageType string   `json:"page_type"`
	Aliases  []string `json:"aliases"`
}

type WikiPageRepo interface {
	// 读
	GetBySlug(ctx context.Context, kbID int64, slug string) (*WikiPage, error)
	GetBySlugs(ctx context.Context, kbID int64, slugs []string) ([]WikiPage, error)
	ListByKBAndType(ctx context.Context, kbID int64, pageType WikiPageType) ([]WikiPage, error)
	ListAllByKB(ctx context.Context, kbID int64) ([]WikiPage, error)
	FullTextSearch(ctx context.Context, kbID int64, query string, limit int) ([]WikiPage, error)
	RegexSearch(ctx context.Context, kbID int64, query string, limit int) ([]WikiPage, error)
	// FindSimilarPages returns the top-k entity/concept pages whose
	// lowercase title is most similar to the given query under pg_trgm
	// trigram similarity. Used by the wiki ingest dedup pre-filter.
	FindSimilarPages(ctx context.Context, kbID int64, query string, pageTypes []string, limit int) ([]WikiPageLite, error)

	// 写
	Upsert(ctx context.Context, page *WikiPage) error
	SoftDelete(ctx context.Context, kbID int64, slug string) error
	DeleteByKB(ctx context.Context, kbID int64) error

	// 引用维护
	UpdateOutLinks(ctx context.Context, kbID int64, slug string, outLinks []string) error
	UpdateInLinks(ctx context.Context, kbID int64, slug string, inLinks []string) error
	AddInLink(ctx context.Context, kbID int64, slug string, fromSlug string) error
	RemoveInLink(ctx context.Context, kbID int64, slug string, fromSlug string) error

	// Issue
	CreateIssue(ctx context.Context, issue *WikiPageIssue) error
	GetIssue(ctx context.Context, issueID int64) (*WikiPageIssue, error)
	ListIssuesByKB(ctx context.Context, kbID int64, status string) ([]WikiPageIssue, error)
	UpdateIssueStatus(ctx context.Context, issueID int64, status string, note string) error
}
