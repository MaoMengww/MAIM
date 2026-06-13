package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/snowflake"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WikiPageRepo struct {
	db     *database.DB
	snowID *snowflake.Node
}

func NewWikiPageRepo(db *database.DB, snow *snowflake.Node) *WikiPageRepo {
	return &WikiPageRepo{db: db, snowID: snow}
}

func (r *WikiPageRepo) GetBySlug(ctx context.Context, kbID int64, slug string) (*domain.WikiPage, error) {
	var page domain.WikiPage
	err := r.db.WithContext(ctx).Where("knowledge_base_id = ? AND slug = ?", kbID, slug).First(&page).Error
	if err != nil {
		return nil, err
	}
	return &page, nil
}

func (r *WikiPageRepo) GetBySlugs(ctx context.Context, kbID int64, slugs []string) ([]domain.WikiPage, error) {
	var pages []domain.WikiPage
	err := r.db.WithContext(ctx).Where("knowledge_base_id = ? AND slug IN ?", kbID, slugs).Find(&pages).Error
	return pages, err
}

func (r *WikiPageRepo) ListByKBAndType(ctx context.Context, kbID int64, pageType domain.WikiPageType) ([]domain.WikiPage, error) {
	var pages []domain.WikiPage
	err := r.db.WithContext(ctx).Where("knowledge_base_id = ? AND page_type = ?", kbID, pageType).Find(&pages).Error
	return pages, err
}

func (r *WikiPageRepo) ListAllByKB(ctx context.Context, kbID int64) ([]domain.WikiPage, error) {
	var pages []domain.WikiPage
	err := r.db.WithContext(ctx).Where("knowledge_base_id = ?", kbID).Find(&pages).Error
	return pages, err
}

func (r *WikiPageRepo) FullTextSearch(ctx context.Context, kbID int64, query string, limit int) ([]domain.WikiPage, error) {
	var pages []domain.WikiPage
	rankOrder := fmt.Sprintf(
		"ts_rank(to_tsvector('simple', title || ' ' || content), plainto_tsquery('simple', '%s')) DESC",
		query)
	err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND deleted_at IS NULL", kbID).
		Where("to_tsvector('simple', title || ' ' || content) @@ plainto_tsquery('simple', ?)", query).
		Order(rankOrder).
		Limit(limit).
		Find(&pages).Error
	return pages, err
}

func (r *WikiPageRepo) Upsert(ctx context.Context, page *domain.WikiPage) error {
	existing, err := r.GetBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	if err == nil && existing != nil {
		page.ID = existing.ID
		page.Version = existing.Version + 1
		page.CreatedAt = existing.CreatedAt
	} else {
		// Check for soft-deleted record and restore it
		var softDeleted domain.WikiPage
		if sdErr := r.db.WithContext(ctx).Unscoped().
			Where("knowledge_base_id = ? AND slug = ?", page.KnowledgeBaseID, page.Slug).
			First(&softDeleted).Error; sdErr == nil {
			page.ID = softDeleted.ID
			page.Version = softDeleted.Version + 1
			page.CreatedAt = softDeleted.CreatedAt
			page.DeletedAt = gorm.DeletedAt{} // clear deleted_at to restore
		} else {
			if page.ID == 0 {
				pageID, err := r.snowID.Generate()
			if err != nil {
				return fmt.Errorf("generate wiki page id failed: %w", err)
			}
			page.ID = pageID
			}
			page.Version = 1
		}
	}
	if page.Status == "" {
		page.Status = domain.WikiPagePublished
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "knowledge_base_id"}, {Name: "slug"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"title", "page_type", "status", "content", "summary",
				"aliases", "source_refs", "chunk_refs",
				"in_links", "out_links", "page_metadata", "version",
				"updated_at", "deleted_at",
			}),
		}).
		Create(page).Error
}

// FindSimilarPages returns the top-k entity/concept pages whose lowercase
// title is most similar to the given query under pg_trgm trigram similarity.
// pageTypes controls which page_type values to include; empty defaults to
// entity+concept. limit is clamped to [1, 50].
func (r *WikiPageRepo) FindSimilarPages(ctx context.Context, kbID int64, query string, pageTypes []string, limit int) ([]domain.WikiPageLite, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	if len(pageTypes) == 0 {
		pageTypes = []string{"entity", "concept"}
	}

	var rows []struct {
		Slug    string `gorm:"column:slug"`
		Title   string `gorm:"column:title"`
		PageType string `gorm:"column:page_type"`
		Aliases string `gorm:"column:aliases"` // JSONB raw
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if err := r.db.WithContext(ctx).
		Table("knowledge.wiki_pages").
		Select("slug, title, page_type, aliases").
		Where("knowledge_base_id = ? AND page_type IN ? AND deleted_at IS NULL AND lower(title) % ?",
			kbID, pageTypes, q).
		Order(fmt.Sprintf("similarity(lower(title), '%s') DESC", q)).
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]domain.WikiPageLite, len(rows))
	for i, row := range rows {
		out[i].Slug = row.Slug
		out[i].Title = row.Title
		out[i].PageType = row.PageType
		if row.Aliases != "" {
			json.Unmarshal([]byte(row.Aliases), &out[i].Aliases)
		}
	}
	return out, nil
}

func (r *WikiPageRepo) RegexSearch(ctx context.Context, kbID int64, query string, limit int) ([]domain.WikiPage, error) {
	var pages []domain.WikiPage
	q := r.db.WithContext(ctx).Where("deleted_at IS NULL")
	if kbID > 0 {
		q = q.Where("knowledge_base_id = ?", kbID)
	}
	q = q.Where("content ~* ? OR title ~* ? OR slug ~* ?", query, query, query)
	err := q.Order("updated_at DESC").Limit(limit).Find(&pages).Error
	return pages, err
}

func (r *WikiPageRepo) GetIssue(ctx context.Context, issueID int64) (*domain.WikiPageIssue, error) {
	var issue domain.WikiPageIssue
	err := r.db.WithContext(ctx).First(&issue, issueID).Error
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

func (r *WikiPageRepo) SoftDelete(ctx context.Context, kbID int64, slug string) error {
	return r.db.WithContext(ctx).Where("knowledge_base_id = ? AND slug = ?", kbID, slug).Delete(&domain.WikiPage{}).Error
}

func (r *WikiPageRepo) DeleteByKB(ctx context.Context, kbID int64) error {
	return r.db.WithContext(ctx).Where("knowledge_base_id = ?", kbID).Delete(&domain.WikiPage{}).Error
}

func (r *WikiPageRepo) UpdateOutLinks(ctx context.Context, kbID int64, slug string, outLinks []string) error {
	data, _ := json.Marshal(outLinks)
	return r.db.WithContext(ctx).Model(&domain.WikiPage{}).
		Where("knowledge_base_id = ? AND slug = ?", kbID, slug).
		Update("out_links", string(data)).Error
}

func (r *WikiPageRepo) UpdateInLinks(ctx context.Context, kbID int64, slug string, inLinks []string) error {
	data, _ := json.Marshal(inLinks)
	return r.db.WithContext(ctx).Model(&domain.WikiPage{}).
		Where("knowledge_base_id = ? AND slug = ?", kbID, slug).
		Update("in_links", string(data)).Error
}

func (r *WikiPageRepo) AddInLink(ctx context.Context, kbID int64, slug string, fromSlug string) error {
	var page domain.WikiPage
	if err := r.db.WithContext(ctx).Where("knowledge_base_id = ? AND slug = ?", kbID, slug).First(&page).Error; err != nil {
		return err
	}
	var links []string
	if len(page.InLinks) > 0 {
		json.Unmarshal(page.InLinks, &links)
	}
	for _, l := range links {
		if l == fromSlug {
			return nil
		}
	}
	links = append(links, fromSlug)
	data, _ := json.Marshal(links)
	return r.db.WithContext(ctx).Model(&domain.WikiPage{}).
		Where("knowledge_base_id = ? AND slug = ?", kbID, slug).
		Update("in_links", string(data)).Error
}

func (r *WikiPageRepo) RemoveInLink(ctx context.Context, kbID int64, slug string, fromSlug string) error {
	var page domain.WikiPage
	if err := r.db.WithContext(ctx).Where("knowledge_base_id = ? AND slug = ?", kbID, slug).First(&page).Error; err != nil {
		return err
	}
	var links []string
	if len(page.InLinks) > 0 {
		json.Unmarshal(page.InLinks, &links)
	}
	filtered := make([]string, 0, len(links))
	for _, l := range links {
		if l != fromSlug {
			filtered = append(filtered, l)
		}
	}
	data, _ := json.Marshal(filtered)
	return r.db.WithContext(ctx).Model(&domain.WikiPage{}).
		Where("knowledge_base_id = ? AND slug = ?", kbID, slug).
		Update("in_links", string(data)).Error
}

func (r *WikiPageRepo) CreateIssue(ctx context.Context, issue *domain.WikiPageIssue) error {
	if issue.ID == 0 {
		issueID, err := r.snowID.Generate()
		if err != nil {
			return fmt.Errorf("generate issue id failed: %w", err)
		}
		issue.ID = issueID
	}
	if issue.Status == "" {
		issue.Status = "open"
	}
	return r.db.WithContext(ctx).Create(issue).Error
}

func (r *WikiPageRepo) ListIssuesByKB(ctx context.Context, kbID int64, status string) ([]domain.WikiPageIssue, error) {
	var issues []domain.WikiPageIssue
	q := r.db.WithContext(ctx).Where("knowledge_base_id = ?", kbID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Order("created_at DESC").Find(&issues).Error
	return issues, err
}

func (r *WikiPageRepo) UpdateIssueStatus(ctx context.Context, issueID int64, status string, note string) error {
	updates := map[string]any{"status": status}
	if status == "resolved" {
		now := time.Now()
		updates["resolved_at"] = &now
	}
	return r.db.WithContext(ctx).Model(&domain.WikiPageIssue{}).Where("id = ?", issueID).Updates(updates).Error
}

var _ domain.WikiPageRepo = (*WikiPageRepo)(nil)
