package domain

import "context"

type KBRepo interface {
	Create(ctx context.Context, kb *KnowledgeBase) error
	Update(ctx context.Context, kb *KnowledgeBase) error
	Delete(ctx context.Context, kbID string) error
	Get(ctx context.Context, kbID string) (*KnowledgeBase, error)
	ListByOwner(ctx context.Context, ownerID string, offset, limit int) ([]KnowledgeBase, int64, error)
	ListByMode(ctx context.Context, mode string, offset, limit int) ([]KnowledgeBase, error)
	ResolveModelID(ctx context.Context, modelName string) (string, error)
	Bind(ctx context.Context, binding *KnowledgeBinding) error
	Unbind(ctx context.Context, kbID string, targetType string, targetID string) error
	ListBindingsByTarget(ctx context.Context, targetType string, targetID string) ([]KnowledgeBinding, error)
	ListBindingsByKB(ctx context.Context, kbID string) ([]KnowledgeBinding, error)
	UpdateCounts(ctx context.Context, kbID string) error
}
