package memory

import (
	"context"
	"fmt"

	"github.com/maomeng/aim/app/ai-bot-service/internal/graph"
)

// GraphStoreAdapter adapts memory.Manager to graph.MemoryStore.
type GraphStoreAdapter struct {
	manager *Manager
}

func NewGraphStoreAdapter(manager *Manager) *GraphStoreAdapter {
	return &GraphStoreAdapter{manager: manager}
}

func (a *GraphStoreAdapter) Retrieve(ctx context.Context, botID, userID int64, ownerID int64, embeddingModelID int64, query string, limit int) ([]graph.MemoryItem, error) {
	if a == nil || a.manager == nil {
		return nil, nil
	}
	items, err := a.manager.Search(ctx, MemoryQuery{
		BotID:            botID,
		UserID:           userID,
		OwnerID:          ownerID,
		EmbeddingModelID: embeddingModelID,
		Query:            query,
		Limit:            limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]graph.MemoryItem, 0, len(items))
	historical := isHistoricalQuery(query)
	for _, item := range items {
		result = append(result, graph.MemoryItem{
			ID:         item.ID,
			Content:    formatMemoryContent(item, historical),
			Type:       "fact",
			Importance: item.Importance,
		})
	}
	return result, nil
}

func (a *GraphStoreAdapter) GetProfile(ctx context.Context, botID, userID int64) string {
	if a == nil || a.manager == nil {
		return ""
	}
	return a.manager.GetProfile(ctx, botID, userID)
}

func formatMemoryContent(item Memory, historical bool) string {
	if !historical {
		return item.Content
	}
	if item.InvalidAt != nil || item.ExpiredAt != nil || item.TemporalHint == "past" {
		return fmt.Sprintf("过去：%s", item.Content)
	}
	return fmt.Sprintf("当前：%s", item.Content)
}
