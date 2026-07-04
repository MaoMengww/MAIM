package memory

import (
	"context"
	"strings"
)

// EntityResolver resolves extracted entity names against the existing graph.
// Only uses exact name match and alias match — no fuzzy similarity.
type EntityResolver struct {
	store *Neo4jStore
}

// NewEntityResolver creates a new EntityResolver.
func NewEntityResolver(store *Neo4jStore) *EntityResolver {
	return &EntityResolver{store: store}
}

// ResolveResult is the outcome of entity resolution.
type ResolveResult struct {
	Entity   *Entity // matched existing entity, nil if new
	IsNew    bool    // true if a new Entity node should be created
}

// Resolve attempts to match a name+entityType against existing entities.
// Layer 1: exact normalized name match (O(1), unique constraint index)
// Layer 2: alias match
// Returns nil Entity if no match — caller should create a new node.
func (r *EntityResolver) Resolve(ctx context.Context, scope Scope, name, entityType string) (*ResolveResult, error) {
	normalized := normalizeEntityName(name)

	// Layer 1: exact normalized name match
	existing, err := r.store.findExactEntity(ctx, scope, normalized, entityType)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return &ResolveResult{Entity: existing, IsNew: false}, nil
	}

	// Layer 2: alias match
	existing, err = r.store.findByAlias(ctx, scope, normalized)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// Add the new name form as an additional alias
		if !containsAlias(existing.Aliases, name) {
			if err := r.store.addAlias(ctx, scope, existing.NormalizedName, existing.EntityType, name); err != nil {
				return nil, err
			}
		}
		return &ResolveResult{Entity: existing, IsNew: false}, nil
	}

	return &ResolveResult{Entity: nil, IsNew: true}, nil
}

func containsAlias(aliases []string, name string) bool {
	normalized := normalizeEntityName(name)
	for _, a := range aliases {
		if strings.EqualFold(strings.TrimSpace(a), normalized) {
			return true
		}
	}
	return false
}
