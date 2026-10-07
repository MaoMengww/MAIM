package graph

import (
	"context"
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
func (r *KnowledgeResolver) Query(ctx context.Context, query string, botID, convID string) (string, []KnowledgeSource, error) {
	if r.kbClient == nil || query == "" {
		return "", nil, nil
	}

	boundKBs, err := r.kbClient.ListBoundKBs(ctx, botID, convID)
	if err != nil {
		return "", nil, err
	}
	ragKBIDs := make([]string, 0, len(boundKBs))
	kbNames := make(map[string]string, len(boundKBs))
	for _, kb := range boundKBs {
		if kb.Mode != "rag" {
			continue
		}
		ragKBIDs = append(ragKBIDs, kb.KBID)
		kbNames[kb.KBID] = kb.Name
	}
	if len(ragKBIDs) == 0 {
		return "", nil, nil
	}
	docs, err := r.kbClient.Retrieve(ctx, query, botID, convID, 5, ragKBIDs)
	if err != nil {
		return "", nil, err
	}
	sources := make([]KnowledgeSource, 0, len(docs))
	for _, d := range docs {
		content := d.MatchedContent
		if content == "" {
			content = d.Content
		}
		kbName := d.KbName
		if kbName == "" {
			kbName = kbNames[d.KbID]
		}
		sources = append(sources, KnowledgeSource{
			Type:    "rag",
			KbName:  kbName,
			KbID:    d.KbID,
			DocID:   d.DocID,
			ChunkID: d.ChunkID,
			Title:   d.Title,
			Content: content,
		})
	}
	return FormatKnowledge(docs), sources, nil
}
