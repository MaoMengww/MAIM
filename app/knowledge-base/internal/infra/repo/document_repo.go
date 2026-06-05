package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/snowflake"
)

type DocumentRepo struct {
	db     *database.DB
	snowID *snowflake.Node
}

func NewDocumentRepo(db *database.DB) *DocumentRepo {
	return &DocumentRepo{db: db}
}

func (r *DocumentRepo) WithSnow(snow *snowflake.Node) *DocumentRepo {
	r.snowID = snow
	return r
}

func (r *DocumentRepo) Create(ctx context.Context, doc *domain.Document) error {
	return r.db.WithContext(ctx).Create(doc).Error
}

func (r *DocumentRepo) Update(ctx context.Context, doc *domain.Document) error {
	doc.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(doc).Where("id = ?", doc.ID).Updates(map[string]any{
		"title":         doc.Title,
		"status":        doc.Status,
		"chunk_count":   doc.ChunkCount,
		"error_message": doc.ErrorMessage,
		"metadata":      doc.Metadata,
		"updated_at":    doc.UpdatedAt,
	}).Error
}

func (r *DocumentRepo) Delete(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).Where("id = ?", docID).Delete(&domain.Document{}).Error
}

func (r *DocumentRepo) Get(ctx context.Context, docID int64) (*domain.Document, error) {
	var doc domain.Document
	err := r.db.WithContext(ctx).Where("id = ?", docID).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r *DocumentRepo) ListByKB(ctx context.Context, kbID int64, offset, limit int, status string) ([]domain.Document, int64, error) {
	var docs []domain.Document
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Document{}).Where("kb_id = ?", kbID)
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&docs).Error
	if err != nil {
		return nil, 0, err
	}
	return docs, total, nil
}

func (r *DocumentRepo) UpdateStatus(ctx context.Context, docID int64, status domain.DocStatus, errMsg string) error {
	return r.db.WithContext(ctx).Model(&domain.Document{}).Where("id = ?", docID).Updates(map[string]any{
		"status":        status,
		"error_message": errMsg,
		"updated_at":    time.Now(),
	}).Error
}

func (r *DocumentRepo) UpdateStages(ctx context.Context, docID int64, stages []domain.Stage) error {
	return r.db.WithContext(ctx).Model(&domain.Document{}).Where("id = ?", docID).Updates(map[string]any{
		"stages":     stages,
		"updated_at": time.Now(),
	}).Error
}

func (r *DocumentRepo) CreateChunks(ctx context.Context, chunks []domain.ChunkRecord) error {
	return r.db.WithContext(ctx).Create(&chunks).Error
}

func (r *DocumentRepo) CreateParentChunk(ctx context.Context, chunk *domain.ChunkRecord) error {
	return r.db.WithContext(ctx).Create(chunk).Error
}

func (r *DocumentRepo) GetParentChunksByDocID(ctx context.Context, docID int64) ([]domain.ChunkRecord, error) {
	var chunks []domain.ChunkRecord
	err := r.db.WithContext(ctx).Where("doc_id = ? AND parent_chunk_id IS NULL", docID).Find(&chunks).Error
	return chunks, err
}

func (r *DocumentRepo) DeleteChunksByDoc(ctx context.Context, docID int64) error {
	return r.db.WithContext(ctx).Where("doc_id = ?", docID).Delete(&domain.ChunkRecord{}).Error
}

func (r *DocumentRepo) GetChunksByDocID(ctx context.Context, docID int64, offset, limit int) ([]domain.ChunkRecord, error) {
	var chunks []domain.ChunkRecord
	query := r.db.WithContext(ctx).Where("doc_id = ? AND (metadata IS NULL OR NOT (metadata @> ?))", docID, `{"block_type": "parent"}`).Order("chunk_index")
	if limit > 0 {
		query = query.Offset(offset).Limit(limit)
	}
	err := query.Find(&chunks).Error
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

func (r *DocumentRepo) CountChunksByDocID(ctx context.Context, docID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&domain.ChunkRecord{}).
		Where("doc_id = ? AND (metadata IS NULL OR NOT (metadata @> ?))", docID, `{"block_type": "parent"}`).
		Count(&total).Error
	if err != nil {
		return 0, err
	}
	return total, nil
}
