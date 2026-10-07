package client

import (
	"context"

	"github.com/maomeng/aim/app/bot-service/internal/graph"
	knowledgebase "github.com/maomeng/aim/app/knowledge-base/pb/knowledgebase"
	"github.com/zeromicro/go-zero/zrpc"
)

// KnowledgeClient is the gRPC client wrapper for knowledge-base.
type KnowledgeClient struct {
	cli knowledgebase.KnowledgeBaseClient
}

// NewKnowledgeClient creates a new knowledge-base client wrapper.
func NewKnowledgeClient(c zrpc.Client) *KnowledgeClient {
	return &KnowledgeClient{
		cli: knowledgebase.NewKnowledgeBaseClient(c.Conn()),
	}
}

// Retrieve fetches relevant knowledge chunks for a query.
func (c *KnowledgeClient) Retrieve(ctx context.Context, query string, botID, convID string, topK int, kbIDs []string) ([]graph.KbDocument, error) {
	resp, err := c.cli.Retrieve(ctx, &knowledgebase.RetrieveReq{
		Query:  query,
		BotId:  optionalID(botID),
		ConvId: optionalID(convID),
		KbIds:  kbIDs,
	})
	if err != nil {
		return nil, err
	}

	docs := make([]graph.KbDocument, 0, len(resp.Items))
	for _, item := range resp.Items {
		docs = append(docs, graph.KbDocument{
			ChunkID:        item.ChunkId,
			DocID:          item.DocId,
			Title:          item.DocTitle,
			Content:        item.Content,
			MatchedContent: item.MatchedContent,
			Score:          float64(item.Score),
			KbID:           item.KbId,
			KbName:         item.KbName,
		})
	}
	return docs, nil
}

// ListBoundKBs returns all KBs bound to a bot or conversation, with mode info.
func (c *KnowledgeClient) ListBoundKBs(ctx context.Context, botID, convID string) ([]graph.BoundKB, error) {
	var seen = make(map[string]bool)
	var result []graph.BoundKB

	addBindings := func(targetType string, targetID string) error {
		if targetID == "" {
			return nil
		}
		resp, err := c.cli.ListBindings(ctx, &knowledgebase.ListBindingsReq{
			TargetType: targetType,
			TargetId:   targetID,
		})
		if err != nil {
			return err
		}
		for _, item := range resp.Items {
			if seen[item.KbId] {
				continue
			}
			seen[item.KbId] = true
			mode := item.Mode
			if mode == "" {
				mode = "rag"
			}
			result = append(result, graph.BoundKB{
				KBID: item.KbId,
				Mode: mode,
				Name: item.KbName,
			})
		}
		return nil
	}

	if err := addBindings("bot", botID); err != nil {
		return nil, err
	}
	if err := addBindings("conv", convID); err != nil {
		return nil, err
	}
	return result, nil
}
