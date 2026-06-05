package domain

import "context"

type KBRepo interface {
	Create(ctx context.Context, kb *KnowledgeBase) error
	Update(ctx context.Context, kb *KnowledgeBase) error
	Delete(ctx context.Context, kbID int64) error
	Get(ctx context.Context, kbID int64) (*KnowledgeBase, error)
	ListByOwner(ctx context.Context, ownerID int64, offset, limit int) ([]KnowledgeBase, int64, error)
	ListByMode(ctx context.Context, mode string, offset, limit int) ([]KnowledgeBase, error)
	Bind(ctx context.Context, binding *KnowledgeBinding) error
	Unbind(ctx context.Context, kbID int64, targetType string, targetID int64) error
	ListBindingsByTarget(ctx context.Context, targetType string, targetID int64) ([]KnowledgeBinding, error)
	ListBindingsByKB(ctx context.Context, kbID int64) ([]KnowledgeBinding, error)
	UpdateCounts(ctx context.Context, kbID int64) error
}
