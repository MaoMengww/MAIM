package neo4j

import (
	"context"
	"errors"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

var ErrNeo4jNotConfigured = errors.New("neo4j not configured")

type GraphNode struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	PageType      string `json:"page_type"`
	Group         string `json:"group"`
	Summary       string `json:"summary"`
	CitationCount int    `json:"citation_count"`
}

type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Weight int    `json:"weight"`
}

type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type GraphStore interface {
	SyncNode(ctx context.Context, kbID int64, slug, title, pageType string) error
	SyncRelationships(ctx context.Context, kbID int64, slug string, targets []string) error
	GetGraph(ctx context.Context, kbID int64) (*GraphData, error)
	Close(ctx context.Context) error
}

type neo4jStore struct {
	driver neo4j.DriverWithContext
}

func NewGraphStore(cfg config.Neo4jConfig) (GraphStore, error) {
	driver, err := neo4j.NewDriverWithContext(cfg.URI, neo4j.BasicAuth(cfg.Username, cfg.Password, ""))
	if err != nil {
		return nil, err
	}
	if err := driver.VerifyConnectivity(context.Background()); err != nil {
		driver.Close(context.Background())
		return nil, err
	}
	return &neo4jStore{driver: driver}, nil
}

func (s *neo4jStore) SyncNode(ctx context.Context, kbID int64, slug, title, pageType string) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return tx.Run(ctx, `
			MERGE (p:WikiPage {slug: $slug, kbID: $kbID})
			SET p.title = $title, p.pageType = $pageType
		`, map[string]any{
			"slug":     slug,
			"kbID":     kbID,
			"title":    title,
			"pageType": pageType,
		})
	})
	return err
}

func (s *neo4jStore) SyncRelationships(ctx context.Context, kbID int64, slug string, targets []string) error {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if _, err := tx.Run(ctx, `
			MATCH (p:WikiPage {slug: $slug, kbID: $kbID})-[r:REFERENCES]->()
			DELETE r
		`, map[string]any{"slug": slug, "kbID": kbID}); err != nil {
			return nil, err
		}
		for _, target := range targets {
			if _, err := tx.Run(ctx, `
				MATCH (a:WikiPage {slug: $slug, kbID: $kbID})
				MATCH (b:WikiPage {slug: $target, kbID: $kbID})
				MERGE (a)-[:REFERENCES]->(b)
			`, map[string]any{
				"slug":   slug,
				"target": target,
				"kbID":   kbID,
			}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

func (s *neo4jStore) GetGraph(ctx context.Context, kbID int64) (*GraphData, error) {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)
	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		records, err := tx.Run(ctx, `
			MATCH (p:WikiPage {kbID: $kbID})
			OPTIONAL MATCH (p)-[r:REFERENCES]->(t:WikiPage {kbID: $kbID})
			WITH p, collect(DISTINCT t.slug) AS targets
			OPTIONAL MATCH (incoming)-[:REFERENCES]->(p)
			RETURN p.slug AS slug, p.title AS title, p.pageType AS pageType,
			       targets, count(DISTINCT incoming) AS citationCount
		`, map[string]any{"kbID": kbID})
		if err != nil {
			return nil, err
		}
		graph := &GraphData{}
		for records.Next(ctx) {
			record := records.Record()
			slug, _ := record.Get("slug")
			title, _ := record.Get("title")
			pageType, _ := record.Get("pageType")
			targets, _ := record.Get("targets")
			citationRaw, _ := record.Get("citationCount")
			slugStr := slug.(string)
			titleStr, _ := title.(string)
			ptStr, _ := pageType.(string)
			citationCount := 0
			if c, ok := citationRaw.(int64); ok {
				citationCount = int(c)
			}
			graph.Nodes = append(graph.Nodes, GraphNode{
				ID: slugStr, Title: titleStr, PageType: ptStr, Group: ptStr,
				CitationCount: citationCount,
			})
			for _, t := range targets.([]any) {
				targetSlug := t.(string)
				graph.Edges = append(graph.Edges, GraphEdge{Source: slugStr, Target: targetSlug})
			}
		}
		if records.Err() != nil {
			return nil, records.Err()
		}
		return graph, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*GraphData), nil
}

func (s *neo4jStore) Close(ctx context.Context) error {
	return s.driver.Close(ctx)
}

// noopGraphStore is used when Neo4j is not configured.
type noopGraphStore struct{}

func NewNoopGraphStore() GraphStore { return &noopGraphStore{} }

func (n *noopGraphStore) SyncNode(_ context.Context, _ int64, _, _, _ string) error {
	return nil
}
func (n *noopGraphStore) SyncRelationships(_ context.Context, _ int64, _ string, _ []string) error {
	return nil
}
func (n *noopGraphStore) GetGraph(_ context.Context, _ int64) (*GraphData, error) {
	return nil, ErrNeo4jNotConfigured
}
func (n *noopGraphStore) Close(_ context.Context) error { return nil }
