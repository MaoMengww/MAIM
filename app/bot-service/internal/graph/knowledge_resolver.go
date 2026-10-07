package graph

import (
	"context"
	"strings"
)

// KnowledgeResolver resolves knowledge from bound RAG KBs.
// It retrieves all bound KBs for a bot+conv and queries them accordingly.
type KnowledgeResolver struct {
	kbClient KbClient
}

// NewKnowledgeResolver creates a new KnowledgeResolver.
func NewKnowledgeResolver(kbClient KbClient) *KnowledgeResolver {
	return &KnowledgeResolver{kbClient: kbClient}
}

// Query retrieves knowledge from all bound KBs and returns formatted context + structured sources.
func (r *KnowledgeResolver) Query(ctx context.Context, query string, botID, convID string) (string, []KnowledgeSource) {
	if r.kbClient == nil || query == "" {
		return "", nil
	}

	boundKBs, err := r.kbClient.ListBoundKBs(ctx, botID, convID)
	if err != nil || len(boundKBs) == 0 {
		return "", nil
	}

	ragKBIDs := make([]string, 0, len(boundKBs))
	for _, kb := range boundKBs {
		ragKBIDs = append(ragKBIDs, kb.KBID)
	}

	var parts []string
	var sources []KnowledgeSource

	if len(ragKBIDs) > 0 {
		docs, err := r.kbClient.Retrieve(ctx, query, botID, convID, 5, ragKBIDs)
		if err == nil && len(docs) > 0 {
			parts = append(parts, FormatKnowledge(docs))
			for _, d := range docs {
				content := d.MatchedContent
				if content == "" {
					content = d.Content
				}
				sources = append(sources, KnowledgeSource{
					Type:    "rag",
					KbName:  d.KbName,
					KbID:    d.KbID,
					Title:   d.Title,
					Content: content,
				})
			}
		}
	}

	return strings.Join(parts, "\n\n"), sources
}
