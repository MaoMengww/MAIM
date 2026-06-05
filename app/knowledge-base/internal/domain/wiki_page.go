package domain

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/maomeng/aim/pkg/errors"
	"gorm.io/gorm"
)

var ErrWikiPageNotFound = errors.New(errors.CodeNotFound, "wiki page not found")

type WikiPageType string

const (
	WikiPageSummary    WikiPageType = "summary"
	WikiPageEntity     WikiPageType = "entity"
	WikiPageConcept    WikiPageType = "concept"
	WikiPageIndex      WikiPageType = "index"
	WikiPageLog        WikiPageType = "log"
	WikiPageSynthesis  WikiPageType = "synthesis"
	WikiPageComparison WikiPageType = "comparison"
)

type WikiPageStatus string

const (
	WikiPageDraft     WikiPageStatus = "draft"
	WikiPagePublished WikiPageStatus = "published"
	WikiPageArchived  WikiPageStatus = "archived"
)

type WikiPage struct {
	ID              int64           `json:"id" gorm:"primaryKey"`
	KnowledgeBaseID int64           `json:"knowledge_base_id" gorm:"uniqueIndex:idx_kb_slug;not null"`
	Slug            string          `json:"slug" gorm:"type:varchar(255);uniqueIndex:idx_kb_slug;not null"`
	Title           string          `json:"title" gorm:"type:varchar(512);not null"`
	PageType        WikiPageType    `json:"page_type" gorm:"type:varchar(32);index;not null"`
	Status          WikiPageStatus  `json:"status" gorm:"type:varchar(32);not null;default:published"`
	Content         string          `json:"content" gorm:"type:text;not null"`
	Summary         string          `json:"summary" gorm:"type:text"`
	Aliases         json.RawMessage `json:"aliases" gorm:"type:jsonb;default:'[]'"`
	SourceRefs      json.RawMessage `json:"source_refs" gorm:"type:jsonb;default:'[]'"`
	ChunkRefs       json.RawMessage `json:"chunk_refs" gorm:"type:jsonb;default:'[]'"`
	InLinks         json.RawMessage `json:"in_links" gorm:"type:jsonb;default:'[]'"`
	OutLinks        json.RawMessage `json:"out_links" gorm:"type:jsonb;default:'[]'"`
	PageMetadata    json.RawMessage `json:"page_metadata" gorm:"type:jsonb;default:'{}'"`
	Version         int             `json:"version" gorm:"not null;default:1"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	DeletedAt       gorm.DeletedAt  `json:"deleted_at" gorm:"index"`
}

func (WikiPage) TableName() string { return "wiki_pages" }

func (p *WikiPage) MilvusDocID() string {
	return "wiki_" + strconv.FormatInt(p.KnowledgeBaseID, 10) + "_" + p.Slug
}

type WikiIssueLevel string

const (
	WikiIssueInfo    WikiIssueLevel = "info"
	WikiIssueWarning WikiIssueLevel = "warning"
	WikiIssueError   WikiIssueLevel = "error"
)

type WikiPageIssue struct {
	ID              int64          `json:"id" gorm:"primaryKey"`
	KnowledgeBaseID int64          `json:"knowledge_base_id" gorm:"index;not null"`
	PageSlug        string         `json:"page_slug" gorm:"type:varchar(255);index;not null"`
	IssueType       string         `json:"issue_type" gorm:"type:varchar(64);not null"`
	Level           WikiIssueLevel `json:"level" gorm:"type:varchar(16);not null;default:warning"`
	Title           string         `json:"title" gorm:"type:varchar(512);not null"`
	Description     string         `json:"description" gorm:"type:text"`
	Status          string         `json:"status" gorm:"type:varchar(16);not null;default:open"`
	CreatedAt       time.Time      `json:"created_at"`
	ResolvedAt      *time.Time     `json:"resolved_at"`
}

func (WikiPageIssue) TableName() string { return "wiki_page_issues" }
