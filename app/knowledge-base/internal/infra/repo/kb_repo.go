package repo

import (
	"context"
	"time"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/database"
	"github.com/maomeng/aim/pkg/identity"
	"github.com/maomeng/aim/pkg/snowflake"
)

type KBRepo struct {
	db     *database.DB
	snowID *snowflake.Node
}

func NewKBRepo(db *database.DB) *KBRepo {
	return &KBRepo{db: db}
}

func (r *KBRepo) WithSnow(snow *snowflake.Node) *KBRepo {
	r.snowID = snow
	return r
}

func (r *KBRepo) Create(ctx context.Context, kb *domain.KnowledgeBase) error {
	if err := kb.ValidateOwner(); err != nil {
		return err
	}
	if kb.EmbeddingModelID != nil {
		if err := identity.Validate(*kb.EmbeddingModelID); err != nil {
			return err
		}
	}
	if err := kb.PipelineConfig.ValidateModelReferences(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(kb).Error
}

func (r *KBRepo) Update(ctx context.Context, kb *domain.KnowledgeBase) error {
	if err := kb.ValidateOwner(); err != nil {
		return err
	}
	if kb.EmbeddingModelID != nil {
		if err := identity.Validate(*kb.EmbeddingModelID); err != nil {
			return err
		}
	}
	if err := kb.PipelineConfig.ValidateModelReferences(); err != nil {
		return err
	}
	kb.UpdatedAt = time.Now()
	updates := map[string]any{
		"name":               kb.Name,
		"description":        kb.Description,
		"embedding_model":    kb.EmbeddingModel,
		"embedding_model_id": kb.EmbeddingModelID,
		"pipeline_config":    kb.PipelineConfig,
		"status":             kb.Status,
		"updated_at":         kb.UpdatedAt,
	}
	if kb.LastMaintenanceAt != nil {
		updates["last_maintenance_at"] = kb.LastMaintenanceAt
	}
	return r.db.WithContext(ctx).Model(kb).Where("id = ?", kb.ID).Updates(updates).Error
}

func (r *KBRepo) ListByMode(ctx context.Context, mode string, offset, limit int) ([]domain.KnowledgeBase, error) {
	var kbs []domain.KnowledgeBase
	err := r.db.WithContext(ctx).Model(&domain.KnowledgeBase{}).
		Where("mode = ?", mode).
		Order("created_at DESC").
		Offset(offset).Limit(limit).
		Find(&kbs).Error
	if err != nil {
		return nil, err
	}
	return kbs, nil
}

func (r *KBRepo) Delete(ctx context.Context, kbID int64) error {
	return r.db.WithContext(ctx).Where("id = ?", kbID).Delete(&domain.KnowledgeBase{}).Error
}

func (r *KBRepo) Get(ctx context.Context, kbID int64) (*domain.KnowledgeBase, error) {
	var kb domain.KnowledgeBase
	err := r.db.WithContext(ctx).Where("id = ?", kbID).First(&kb).Error
	if err != nil {
		return nil, err
	}
	return &kb, nil
}

func (r *KBRepo) ListByOwner(ctx context.Context, ownerID string, offset, limit int) ([]domain.KnowledgeBase, int64, error) {
	var kbs []domain.KnowledgeBase
	var total int64
	if err := identity.Validate(ownerID); err != nil {
		return nil, 0, err
	}

	if err := r.db.WithContext(ctx).Model(&domain.KnowledgeBase{}).
		Where("owner_type = 'user' AND owner_id = ?", ownerID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := r.db.WithContext(ctx).Where("owner_type = 'user' AND owner_id = ?", ownerID).
		Order("created_at DESC").Offset(offset).Limit(limit).Find(&kbs).Error
	if err != nil {
		return nil, 0, err
	}
	return kbs, total, nil
}

func (r *KBRepo) ResolveModelID(ctx context.Context, modelName string) (string, error) {
	var entry struct{ ID string }
	err := r.db.WithContext(ctx).
		Table("llm.model_registry").
		Where("model_name = ? AND status = 'active' AND capability = 'embed' AND owner_type = 'platform' AND owner_id IS NULL", modelName).
		Select("id").
		Take(&entry).Error
	if err != nil {
		return "", err
	}
	if err := identity.Validate(entry.ID); err != nil {
		return "", err
	}
	return entry.ID, nil
}

func (r *KBRepo) Bind(ctx context.Context, binding *domain.KnowledgeBinding) error {
	return r.db.WithContext(ctx).Create(binding).Error
}

func (r *KBRepo) Unbind(ctx context.Context, kbID int64, targetType string, targetID int64) error {
	return r.db.WithContext(ctx).
		Where("kb_id = ? AND target_type = ? AND target_id = ?", kbID, targetType, targetID).
		Delete(&domain.KnowledgeBinding{}).Error
}

func (r *KBRepo) ListBindingsByTarget(ctx context.Context, targetType string, targetID int64) ([]domain.KnowledgeBinding, error) {
	var bindings []domain.KnowledgeBinding
	err := r.db.WithContext(ctx).
		Table("knowledge_bindings").
		Select("knowledge_bindings.*, knowledge_bases.name as kb_name").
		Joins("JOIN knowledge_bases ON knowledge_bases.id = knowledge_bindings.kb_id").
		Where("knowledge_bindings.target_type = ? AND knowledge_bindings.target_id = ?", targetType, targetID).
		Find(&bindings).Error
	if err != nil {
		return nil, err
	}
	return bindings, nil
}

func (r *KBRepo) ListBindingsByKB(ctx context.Context, kbID int64) ([]domain.KnowledgeBinding, error) {
	var bindings []domain.KnowledgeBinding
	err := r.db.WithContext(ctx).Where("kb_id = ?", kbID).Find(&bindings).Error
	if err != nil {
		return nil, err
	}
	return bindings, nil
}

func (r *KBRepo) UpdateCounts(ctx context.Context, kbID int64) error {
	var docCount int64
	if err := r.db.WithContext(ctx).Model(&domain.Document{}).Where("kb_id = ?", kbID).Count(&docCount).Error; err != nil {
		return err
	}

	var chunkCount int64
	if err := r.db.WithContext(ctx).Model(&domain.ChunkRecord{}).
		Where("kb_id = ? AND (metadata IS NULL OR NOT (metadata @> ?))", kbID, `{"block_type": "parent"}`).
		Count(&chunkCount).Error; err != nil {
		return err
	}

	return r.db.WithContext(ctx).Model(&domain.KnowledgeBase{}).Where("id = ?", kbID).Updates(map[string]any{
		"doc_count":    docCount,
		"total_chunks": chunkCount,
		"updated_at":   time.Now(),
	}).Error
}
