package graph

import (
	"context"
	"strings"
)

// KnowledgeResolver resolves knowledge from both RAG and Wiki KBs.
// It retrieves all bound KBs for a bot+conv, splits by mode, and queries accordingly.
type KnowledgeResolver struct {
	kbClient KbClient
}

// NewKnowledgeResolver creates a new KnowledgeResolver.
func NewKnowledgeResolver(kbClient KbClient) *KnowledgeResolver {
	return &KnowledgeResolver{kbClient: kbClient}
}

// Query retrieves knowledge from all bound KBs and returns formatted context + structured sources.
func (r *KnowledgeResolver) Query(ctx context.Context, query string, botID, convID int64, modelID int64, modelName string, history string) (string, []KnowledgeSource) {
	if r.kbClient == nil || query == "" {
		return "", nil
	}

	boundKBs, err := r.kbClient.ListBoundKBs(ctx, botID, convID)
	if err != nil || len(boundKBs) == 0 {
		return "", nil
	}

	var ragKBIDs, wikiKBIDs []int64
	for _, kb := range boundKBs {
		switch kb.Mode {
		case "wiki":
			wikiKBIDs = append(wikiKBIDs, kb.KBID)
		default:
			ragKBIDs = append(ragKBIDs, kb.KBID)
		}
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

	if len(wikiKBIDs) > 0 {
		// Build wiki KB name map for source attribution
		wikiKBNameMap := make(map[int64]string)
		for _, kb := range boundKBs {
			if kb.Mode == "wiki" {
				wikiKBNameMap[kb.KBID] = kb.Name
			}
		}
		wikiKBName := ""
		if len(wikiKBIDs) > 0 {
			wikiKBName = wikiKBNameMap[wikiKBIDs[0]]
		}

		result, err := r.kbClient.WikiQuery(ctx, query, wikiKBIDs, modelID, modelName, history)
		if err == nil && result != nil && result.Answer != "" {
			refs := ""
			if len(result.References) > 0 {
				var refTitles []string
				for _, ref := range result.References {
					refTitles = append(refTitles, ref.Slug)
					title := ref.Title
					if title == "" {
						title = ref.Slug
					}
					content := ref.Snippet
					if content == "" {
						content = title
					}
					sources = append(sources, KnowledgeSource{
						Type:    "wiki",
						KbName:  wikiKBName,
						Title:   title,
						Content: content,
					})
				}
				refs = "\nReference pages: " + strings.Join(refTitles, ", ")
			}
			parts = append(parts, "[Wiki Knowledge Base]\n"+result.Answer+refs)
		}
	}

	return strings.Join(parts, "\n\n"), sources
}
