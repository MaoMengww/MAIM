package memory

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"

	"github.com/maomeng/aim/app/ai-bot-service/internal/config"
)

const (
	memoryCollection = "bot_memory_facts_v1"
	denseFieldName   = "dense_vector"
	sparseFieldName  = "sparse_vector"
	defaultDimSize   = 1536
)

// VectorHit is a fact id returned from vector search.
type VectorHit struct {
	FactID int64
	Score  float64
}

// MemoryVectorFilter scopes vector search.
type MemoryVectorFilter struct {
	BotID   int64
	UserID  int64
	Expired *bool
}

// MemoryVectorStore indexes memory facts in Milvus for hybrid retrieval.
type MemoryVectorStore struct {
	cli          *client.Client
	collection   string
	embeddingDim int
}

// NewMemoryVectorStore creates a Milvus-backed memory vector store.
func NewMemoryVectorStore(cfg config.MilvusConfig, collection string, embeddingDim int) (*MemoryVectorStore, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("milvus host is required for memory vector store")
	}
	if collection == "" {
		collection = memoryCollection
	}
	if embeddingDim <= 0 {
		embeddingDim = defaultDimSize
	}
	address := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	cli, err := client.New(context.Background(), &client.ClientConfig{
		Address: address,
		DBName:  cfg.Database,
	})
	if err != nil {
		return nil, fmt.Errorf("create milvus client for memory: %w", err)
	}
	return &MemoryVectorStore{cli: cli, collection: collection, embeddingDim: embeddingDim}, nil
}

// EnsureCollection creates the collection and indexes if they do not exist.
func (s *MemoryVectorStore) EnsureCollection(ctx context.Context) error {
	if s == nil || s.cli == nil {
		return nil
	}
	has, err := s.cli.HasCollection(ctx, client.NewHasCollectionOption(s.collection))
	if err != nil {
		return err
	}
	if has {
		return s.ensureIndexes(ctx)
	}

	schema := &entity.Schema{
		CollectionName: s.collection,
		Description:    "AIM bot memory facts for hybrid retrieval",
		AutoID:         false,
		Fields: []*entity.Field{
			entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64).WithIsPrimaryKey(true),
			entity.NewField().WithName("bot_id").WithDataType(entity.FieldTypeInt64),
			entity.NewField().WithName("user_id").WithDataType(entity.FieldTypeInt64),
			entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).WithMaxLength(65535).WithEnableAnalyzer(true).WithEnableMatch(true),
			entity.NewField().WithName("predicate").WithDataType(entity.FieldTypeVarChar).WithMaxLength(128),
			entity.NewField().WithName("category").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64),
			entity.NewField().WithName("entity_type").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64),
			entity.NewField().WithName("temporal_hint").WithDataType(entity.FieldTypeVarChar).WithMaxLength(16),
			entity.NewField().WithName(denseFieldName).WithDataType(entity.FieldTypeFloatVector).WithDim(int64(s.embeddingDim)),
			entity.NewField().WithName(sparseFieldName).WithDataType(entity.FieldTypeSparseVector),
			entity.NewField().WithName("created_at").WithDataType(entity.FieldTypeInt64),
			entity.NewField().WithName("meta").WithDataType(entity.FieldTypeVarChar).WithMaxLength(65535),
		},
	}

	schema.WithFunction(entity.NewFunction().
		WithName("bm25_fn").
		WithType(entity.FunctionTypeBM25).
		WithInputFields("content").
		WithOutputFields(sparseFieldName),
	)

	indexOpts := []client.CreateIndexOption{
		client.NewCreateIndexOption(s.collection, denseFieldName, index.NewHNSWIndex(entity.COSINE, 16, 200)),
		client.NewCreateIndexOption(s.collection, sparseFieldName, index.NewSparseInvertedIndex(entity.BM25, 0.3)),
	}

	createOpt := client.NewCreateCollectionOption(s.collection, schema).WithIndexOptions(indexOpts...)
	if err := s.cli.CreateCollection(ctx, createOpt); err != nil {
		return fmt.Errorf("create memory collection: %w", err)
	}

	loadTask, err := s.cli.LoadCollection(ctx, client.NewLoadCollectionOption(s.collection))
	if err != nil {
		return fmt.Errorf("load memory collection: %w", err)
	}
	return loadTask.Await(ctx)
}

func (s *MemoryVectorStore) ensureIndexes(ctx context.Context) error {
	existing, err := s.cli.ListIndexes(ctx, client.NewListIndexOption(s.collection))
	if err != nil {
		return fmt.Errorf("list memory indexes: %w", err)
	}

	required := map[string]client.CreateIndexOption{
		denseFieldName:  client.NewCreateIndexOption(s.collection, denseFieldName, index.NewHNSWIndex(entity.COSINE, 16, 200)),
		sparseFieldName: client.NewCreateIndexOption(s.collection, sparseFieldName, index.NewSparseInvertedIndex(entity.BM25, 0.3)),
	}

	hasIndex := make(map[string]bool, len(existing))
	for _, name := range existing {
		hasIndex[name] = true
	}

	created := false
	for fieldName, opt := range required {
		if hasIndex[fieldName] {
			continue
		}
		task, err := s.cli.CreateIndex(ctx, opt)
		if err != nil {
			return fmt.Errorf("create memory index on %s: %w", fieldName, err)
		}
		if err := task.Await(ctx); err != nil {
			return fmt.Errorf("await memory index on %s: %w", fieldName, err)
		}
		created = true
	}

	if created {
		loadTask, err := s.cli.LoadCollection(ctx, client.NewLoadCollectionOption(s.collection))
		if err != nil {
			return fmt.Errorf("load memory collection after index: %w", err)
		}
		return loadTask.Await(ctx)
	}
	return nil
}

// UpsertFacts inserts or updates memory facts in the vector store.
func (s *MemoryVectorStore) UpsertFacts(ctx context.Context, facts []Fact, vectors [][]float32) error {
	if s == nil || s.cli == nil || len(facts) == 0 {
		return nil
	}
	if err := s.EnsureCollection(ctx); err != nil {
		return fmt.Errorf("ensure memory collection: %w", err)
	}

	n := min(len(facts), len(vectors))
	ids := make([]string, n)
	botIDs := make([]int64, n)
	userIDs := make([]int64, n)
	contents := make([]string, n)
	predicates := make([]string, n)
	categories := make([]string, n)
	entityTypes := make([]string, n)
	temporalHints := make([]string, n)
	denseVecs := make([][]float32, n)
	createdAts := make([]int64, n)
	metas := make([]string, n)

	for i := 0; i < n; i++ {
		f := facts[i]
		ids[i] = fmt.Sprintf("%d", f.ID)
		botIDs[i] = f.BotID
		userIDs[i] = f.UserID
		contents[i] = f.Content
		predicates[i] = f.Predicate
		categories[i] = f.Category
		entityTypes[i] = f.EntityType
		temporalHints[i] = f.TemporalHint
		createdAts[i] = f.CreatedAt.Unix()
		denseVecs[i] = padOrTrimVector(vectors[i], s.embeddingDim)
		metas[i] = `{}`
	}

	opt := client.NewColumnBasedInsertOption(s.collection).
		WithVarcharColumn("id", ids).
		WithInt64Column("bot_id", botIDs).
		WithInt64Column("user_id", userIDs).
		WithVarcharColumn("content", contents).
		WithVarcharColumn("predicate", predicates).
		WithVarcharColumn("category", categories).
		WithVarcharColumn("entity_type", entityTypes).
		WithVarcharColumn("temporal_hint", temporalHints).
		WithFloatVectorColumn(denseFieldName, s.embeddingDim, denseVecs).
		WithInt64Column("created_at", createdAts).
		WithVarcharColumn("meta", metas)

	if _, err := s.cli.Upsert(ctx, opt); err != nil {
		return fmt.Errorf("milvus memory upsert: %w", err)
	}

	if _, err := s.cli.Flush(ctx, client.NewFlushOption(s.collection)); err != nil {
		return fmt.Errorf("milvus memory flush: %w", err)
	}

	loadTask, err := s.cli.LoadCollection(ctx, client.NewLoadCollectionOption(s.collection))
	if err != nil {
		return err
	}
	return loadTask.Await(ctx)
}

// HybridSearch runs a hybrid (dense + sparse) search and returns fact ids.
func (s *MemoryVectorStore) HybridSearch(ctx context.Context, vector []float32, query string, topK int, filter MemoryVectorFilter) ([]VectorHit, error) {
	if s == nil || s.cli == nil {
		return nil, nil
	}
	if err := s.EnsureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure memory collection: %w", err)
	}

	expr := buildMemoryFilterExpr(filter)

	denseReq := client.NewAnnRequest(denseFieldName, topK, entity.FloatVector(vector))
	if expr != "" {
		denseReq.WithFilter(expr)
	}

	annReqs := []*client.AnnRequest{denseReq}
	if query != "" {
		sparseReq := client.NewAnnRequest(sparseFieldName, topK, entity.Text(query))
		if expr != "" {
			sparseReq.WithFilter(expr)
		}
		annReqs = append(annReqs, sparseReq)
	}

	hybridOpt := client.NewHybridSearchOption(s.collection, topK, annReqs...).
		WithReranker(client.NewRRFReranker()).
		WithOutputFields("id")

	resultSet, err := s.cli.HybridSearch(ctx, hybridOpt)
	if err != nil {
		return nil, fmt.Errorf("milvus memory hybrid search: %w", err)
	}

	return convertMemoryHits(resultSet), nil
}

func (s *MemoryVectorStore) Close(ctx context.Context) error {
	if s == nil || s.cli == nil {
		return nil
	}
	return s.cli.Close(ctx)
}

func buildMemoryFilterExpr(filter MemoryVectorFilter) string {
	if filter.BotID <= 0 && filter.UserID <= 0 {
		return ""
	}
	var parts []string
	if filter.BotID > 0 {
		parts = append(parts, fmt.Sprintf("bot_id == %d", filter.BotID))
	}
	if filter.UserID > 0 {
		parts = append(parts, fmt.Sprintf("user_id == %d", filter.UserID))
	}
	expr := ""
	for i, p := range parts {
		if i > 0 {
			expr += " && "
		}
		expr += p
	}
	return expr
}

func convertMemoryHits(resultSet []client.ResultSet) []VectorHit {
	var out []VectorHit
	if len(resultSet) == 0 {
		return out
	}
	set := resultSet[0]
	for i := 0; i < set.Len(); i++ {
		score := float64(0)
		if i < len(set.Scores) {
			score = float64(set.Scores[i])
		}
		id, err := getColumnString(set.GetColumn("id"), i)
		if err != nil || id == "" {
			continue
		}
		var factID int64
		fmt.Sscanf(id, "%d", &factID)
		if factID > 0 {
			out = append(out, VectorHit{FactID: factID, Score: score})
		}
	}
	return out
}

func getColumnString(col column.Column, idx int) (string, error) {
	if col == nil || col.Len() <= idx {
		return "", fmt.Errorf("column index out of range")
	}
	return col.GetAsString(idx)
}

func padOrTrimVector(v []float32, dim int) []float32 {
	if len(v) == dim {
		return v
	}
	if len(v) > dim {
		return v[:dim]
	}
	out := make([]float32, dim)
	copy(out, v)
	return out
}
