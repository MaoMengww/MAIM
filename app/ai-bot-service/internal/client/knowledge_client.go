package client

import (
	"context"
	"strconv"

	"github.com/maomeng/aim/app/ai-bot-service/internal/graph"
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
func (c *KnowledgeClient) Retrieve(ctx context.Context, query string, botID, convID int64, topK int, kbIDs []int64) ([]graph.KbDocument, error) {
	resp, err := c.cli.Retrieve(ctx, &knowledgebase.RetrieveReq{
		Query:  query,
		BotId:  botID,
		ConvId: convID,
		KbIds:  kbIDs,
	})
	if err != nil {
		return nil, err
	}

	docs := make([]graph.KbDocument, 0, len(resp.Items))
	for _, item := range resp.Items {
		docs = append(docs, graph.KbDocument{
			DocID:          strconv.FormatInt(item.DocId, 10),
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

// WikiQuery sends a query to wiki knowledge bases and returns the answer.
func (c *KnowledgeClient) WikiQuery(ctx context.Context, query string, wikiKBIDs []int64, modelID int64, modelName string, history string) (*graph.WikiResult, error) {
	resp, err := c.cli.WikiQuery(ctx, &knowledgebase.WikiQueryReq{
		Query:     query,
		WikiKbIds: wikiKBIDs,
		ModelId:   modelID,
		ModelName: modelName,
		History:   history,
	})
	if err != nil {
		return nil, err
	}
	refs := make([]graph.WikiReference, len(resp.Refs))
	for i, r := range resp.Refs {
		refs[i] = graph.WikiReference{
			Slug:    r.Slug,
			Title:   r.Title,
			Snippet: r.Snippet,
		}
	}
	return &graph.WikiResult{
		Answer:     resp.Answer,
		References: refs,
	}, nil
}

// ListBoundKBs returns all KBs bound to a bot or conversation, with mode info.
func (c *KnowledgeClient) ListBoundKBs(ctx context.Context, botID, convID int64) ([]graph.BoundKB, error) {
	var seen = make(map[int64]bool)
	var result []graph.BoundKB

	addBindings := func(targetType string, targetID int64) {
		if targetID <= 0 {
			return
		}
		resp, err := c.cli.ListBindings(ctx, &knowledgebase.ListBindingsReq{
			TargetType: targetType,
			TargetId:   targetID,
		})
		if err != nil {
			return
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
	}

	addBindings("bot", botID)
	addBindings("conv", convID)
	return result, nil
}
