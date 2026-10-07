package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
)

type DocumentRepo struct {
	db *database.DB
}

func NewDocumentRepo(db *database.DB) *DocumentRepo {
	return &DocumentRepo{db: db}
}

func (r *DocumentRepo) Create(ctx context.Context, doc *domain.Document) error {
	if err := identity.Validate(doc.KBID); err != nil {
		return err
	}
	if doc.PipelineOverride != nil {
		if err := doc.PipelineOverride.ValidateModelReferences(); err != nil {
			return err
		}
	}
	if err := assignEntityID(&doc.ID); err != nil {
		return err
	}
	if doc.Stages == nil {
		doc.Stages = []domain.Stage{}
	}
	// The KB row lock makes an upload commit before deletion starts, or fail
	// after deletion starts. Object upload happens outside this transaction.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var kb domain.KnowledgeBase
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "status").Where("id = ?", doc.KBID).First(&kb).Error; err != nil {
			return err
		}
		if kb.Status != "active" {
			return domain.ErrKBNotFound
		}
		return tx.Create(doc).Error
	})
}

func (r *DocumentRepo) Update(ctx context.Context, doc *domain.Document) error {
	if err := identity.Validate(doc.ID); err != nil {
		return err
	}
	metadata, err := json.Marshal(doc.Metadata)
	if err != nil {
		return err
	}
	if doc.Stages == nil {
		doc.Stages = []domain.Stage{}
	}
	stages, err := json.Marshal(doc.Stages)
	if err != nil {
		return err
	}
	doc.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(doc).Where("id = ?", doc.ID).Updates(map[string]any{
		"title":         doc.Title,
		"status":        doc.Status,
		"chunk_count":   doc.ChunkCount,
		"error_message": doc.ErrorMessage,
		"metadata":      gorm.Expr("?::jsonb", string(metadata)),
		"stages":        gorm.Expr("?::jsonb", string(stages)),
		"updated_at":    doc.UpdatedAt,
	}).Error
}

func (r *DocumentRepo) Delete(ctx context.Context, docID string) error {
	if err := identity.Validate(docID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Where("id = ?", docID).Delete(&domain.Document{}).Error
}

func (r *DocumentRepo) Get(ctx context.Context, docID string) (*domain.Document, error) {
	if err := identity.Validate(docID); err != nil {
		return nil, err
	}
	var doc domain.Document
	err := r.db.WithContext(ctx).Where("id = ?", docID).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r *DocumentRepo) GetByHash(ctx context.Context, kbID string, contentHash string) (*domain.Document, error) {
	if err := identity.Validate(kbID); err != nil {
		return nil, err
	}
	var doc domain.Document
	err := r.db.WithContext(ctx).Where("kb_id = ? AND content_hash = ?", kbID, contentHash).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r *DocumentRepo) ListByKB(ctx context.Context, kbID string, offset, limit int, status string) ([]domain.Document, int64, error) {
	if err := identity.Validate(kbID); err != nil {
		return nil, 0, err
	}
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

func (r *DocumentRepo) UpdateStatus(ctx context.Context, docID string, status domain.DocStatus, errMsg string) error {
	if err := identity.Validate(docID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&domain.Document{}).Where("id = ?", docID).Updates(map[string]any{
		"status":        status,
		"error_message": errMsg,
		"updated_at":    time.Now(),
	}).Error
}

func (r *DocumentRepo) UpdateStages(ctx context.Context, docID string, stages []domain.Stage) error {
	if err := identity.Validate(docID); err != nil {
		return err
	}
	if stages == nil {
		stages = []domain.Stage{}
	}
	raw, err := json.Marshal(stages)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&domain.Document{}).Where("id = ?", docID).Updates(map[string]any{
		"stages":     gorm.Expr("?::jsonb", string(raw)),
		"updated_at": time.Now(),
	}).Error
}

func (r *DocumentRepo) CreateChunks(ctx context.Context, chunks []domain.ChunkRecord) error {
	if len(chunks) == 0 {
		return nil
	}
	for i := range chunks {
		if err := prepareChunk(&chunks[i]); err != nil {
			return err
		}
	}
	return r.db.WithContext(ctx).Create(&chunks).Error
}

func (r *DocumentRepo) CreateParentChunk(ctx context.Context, chunk *domain.ChunkRecord) error {
	if err := prepareChunk(chunk); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(chunk).Error
}

func prepareChunk(chunk *domain.ChunkRecord) error {
	if err := identity.Validate(chunk.DocID); err != nil {
		return err
	}
	if err := identity.Validate(chunk.KBID); err != nil {
		return err
	}
	if chunk.ParentChunkID != nil {
		if err := identity.Validate(*chunk.ParentChunkID); err != nil {
			return err
		}
	}
	return assignEntityID(&chunk.ID)
}

func (r *DocumentRepo) GetParentChunksByDocID(ctx context.Context, docID string) ([]domain.ChunkRecord, error) {
	if err := identity.Validate(docID); err != nil {
		return nil, err
	}
	var chunks []domain.ChunkRecord
	err := r.db.WithContext(ctx).
		Where("doc_id = ? AND parent_chunk_id IS NULL AND metadata @> ?", docID, `{"block_type": "parent"}`).
		Order("chunk_index").Find(&chunks).Error
	return chunks, err
}

func (r *DocumentRepo) DeleteChunksByDoc(ctx context.Context, docID string) error {
	if err := identity.Validate(docID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Where("doc_id = ?", docID).Delete(&domain.ChunkRecord{}).Error
}

func (r *DocumentRepo) GetChunksByDocID(ctx context.Context, docID string, offset, limit int) ([]domain.ChunkRecord, error) {
	if err := identity.Validate(docID); err != nil {
		return nil, err
	}
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

func (r *DocumentRepo) CountChunksByDocID(ctx context.Context, docID string) (int64, error) {
	if err := identity.Validate(docID); err != nil {
		return 0, err
	}
	var total int64
	err := r.db.WithContext(ctx).Model(&domain.ChunkRecord{}).
		Where("doc_id = ? AND (metadata IS NULL OR NOT (metadata @> ?))", docID, `{"block_type": "parent"}`).
		Count(&total).Error
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (r *DocumentRepo) GetChunksByIDs(ctx context.Context, ids []string) ([]domain.ChunkRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	for _, id := range ids {
		if err := identity.Validate(id); err != nil {
			return nil, err
		}
	}
	var chunks []domain.ChunkRecord
	err := r.db.WithContext(ctx).Model(&domain.ChunkRecord{}).
		Select("document_chunks.*").
		Joins("JOIN documents ON documents.id = document_chunks.doc_id AND documents.kb_id = document_chunks.kb_id").
		Joins("JOIN knowledge_bases ON knowledge_bases.id = documents.kb_id").
		Where("document_chunks.id IN ? AND documents.status = ? AND knowledge_bases.status = ?", ids, domain.DocStatusReady, "active").
		Find(&chunks).Error
	return chunks, err
}

func (r *DocumentRepo) WithDocumentLock(ctx context.Context, docID string, fn func(context.Context) error) (err error) {
	if err := identity.Validate(docID); err != nil {
		return err
	}
	sqlDB, err := r.db.DB.DB()
	if err != nil {
		return err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	lockName := "knowledge.document:" + docID
	// Cleanup also runs after canceled acquisition: the server may have acquired
	// the session lock just before cancellation reached the client.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		unlockErr := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockName).Scan(&unlocked)
		if unlockErr != nil {
			// Close alone returns a sql.Conn to the pool. Discard a session whose
			// unlock could not be confirmed so no later borrower inherits its lock.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fmt.Errorf("release document lock: %w", unlockErr))
		}
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
			err = errors.Join(err, closeErr)
		}
	}()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockName); err != nil {
		return fmt.Errorf("acquire document lock: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Callback repository calls use the normal pool, not this dedicated session
	// or an open transaction. The lock remains held through callback cleanup.
	return fn(ctx)
}
