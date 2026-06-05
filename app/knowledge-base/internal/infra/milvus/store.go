package milvus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

const (
	collectionName = "kb_chunks_v2"
	denseField     = "dense_vector"
	sparseField    = "sparse_vector"
	defaultDim     = 1536
)

type MilvusStore struct {
	cli *client.Client
}

func NewMilvusStore(cfg config.MilvusConfig) (*MilvusStore, error) {
	if cfg.Address == "" {
		return nil, fmt.Errorf("milvus address is required")
	}
	if cfg.DBName == "" {
		cfg.DBName = "default"
	}
	cli, err := client.New(context.Background(), &client.ClientConfig{
		Address: cfg.Address,
		DBName:  cfg.DBName,
	})
	if err != nil {
		return nil, fmt.Errorf("create milvus client failed: %w", err)
	}
	return &MilvusStore{cli: cli}, nil
}

func (s *MilvusStore) ensureCollection(ctx context.Context) error {
	has, err := s.cli.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil {
		return err
	}
	if has {
		return s.ensureIndexes(ctx)
	}

	schema := &entity.Schema{
		CollectionName: collectionName,
		Description:    "AIM knowledge base chunk embeddings",
		AutoID:         false,
		Fields: []*entity.Field{
			entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64).WithIsPrimaryKey(true),
			entity.NewField().WithName("kb_id").WithDataType(entity.FieldTypeInt64),
			entity.NewField().WithName("doc_id").WithDataType(entity.FieldTypeInt64),
			entity.NewField().WithName("chunk_index").WithDataType(entity.FieldTypeInt32),
			entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).WithMaxLength(65535).WithEnableAnalyzer(true).WithEnableMatch(true),
			entity.NewField().WithName(denseField).WithDataType(entity.FieldTypeFloatVector).WithDim(defaultDim),
			entity.NewField().WithName(sparseField).WithDataType(entity.FieldTypeSparseVector),
			entity.NewField().WithName("chunk_meta").WithDataType(entity.FieldTypeVarChar).WithMaxLength(65535),
			entity.NewField().WithName("created_at").WithDataType(entity.FieldTypeInt64),
		},
	}

	// BM25 function auto-computes sparse_vector from content field
	schema.WithFunction(entity.NewFunction().
		WithName("bm25_fn").
		WithType(entity.FunctionTypeBM25).
		WithInputFields("content").
		WithOutputFields("sparse_vector"),
	)

	indexOpts := []client.CreateIndexOption{
		client.NewCreateIndexOption(collectionName, denseField, index.NewHNSWIndex(entity.COSINE, 16, 200)),
		client.NewCreateIndexOption(collectionName, sparseField, index.NewSparseInvertedIndex(entity.BM25, 0.3)),
	}

	createOpt := client.NewCreateCollectionOption(collectionName, schema).WithIndexOptions(indexOpts...)
	if err := s.cli.CreateCollection(ctx, createOpt); err != nil {
		return fmt.Errorf("create collection failed: %w", err)
	}

	loadTask, err := s.cli.LoadCollection(ctx, client.NewLoadCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("load collection failed: %w", err)
	}
	return loadTask.Await(ctx)
}

// ensureIndexes checks that required indexes exist and creates any missing ones.
func (s *MilvusStore) ensureIndexes(ctx context.Context) error {
	existing, err := s.cli.ListIndexes(ctx, client.NewListIndexOption(collectionName))
	if err != nil {
		return fmt.Errorf("list indexes failed: %w", err)
	}

	required := map[string]client.CreateIndexOption{
		denseField:  client.NewCreateIndexOption(collectionName, denseField, index.NewHNSWIndex(entity.COSINE, 16, 200)),
		sparseField: client.NewCreateIndexOption(collectionName, sparseField, index.NewSparseInvertedIndex(entity.BM25, 0.3)),
	}

	hasIndex := make(map[string]bool, len(existing))
	for _, name := range existing {
		hasIndex[name] = true
	}

	created := false
	for fieldName, opt := range required {
		// The default index name equals the field name in Milvus
		if hasIndex[fieldName] {
			continue
		}
		task, err := s.cli.CreateIndex(ctx, opt)
		if err != nil {
			return fmt.Errorf("create index on %s failed: %w", fieldName, err)
		}
		if err := task.Await(ctx); err != nil {
			return fmt.Errorf("await index on %s failed: %w", fieldName, err)
		}
		created = true
	}

	if created {
		// Reload collection so the new indexes take effect
		loadTask, err := s.cli.LoadCollection(ctx, client.NewLoadCollectionOption(collectionName))
		if err != nil {
			return fmt.Errorf("load collection after index creation failed: %w", err)
		}
		return loadTask.Await(ctx)
	}
	return nil
}

func (s *MilvusStore) Insert(ctx context.Context, docs []domain.VectorDoc) error {
	return s.Upsert(ctx, docs)
}

func (s *MilvusStore) Upsert(ctx context.Context, docs []domain.VectorDoc) error {
	if len(docs) == 0 {
		return nil
	}
	if err := s.ensureCollection(ctx); err != nil {
		return fmt.Errorf("ensure milvus collection: %w", err)
	}

	n := len(docs)
	ids := make([]string, n)
	kbIDs := make([]int64, n)
	docIDs := make([]int64, n)
	chunkIdx := make([]int32, n)
	contents := make([]string, n)
	denseVecs := make([][]float32, n)
	metaStrs := make([]string, n)
	createdAts := make([]int64, n)
	for i, doc := range docs {
		ids[i] = doc.DocID
		kbIDs[i] = doc.KBID
		contents[i] = doc.Content
		createdAts[i] = 0
		if v, ok := doc.Metadata["doc_id"].(int64); ok {
			docIDs[i] = v
		}
		if v, ok := doc.Metadata["chunk_index"].(int32); ok {
			chunkIdx[i] = v
		}
		if doc.Vector != nil && len(doc.Vector) == defaultDim {
			denseVecs[i] = doc.Vector
		} else {
			denseVecs[i] = make([]float32, defaultDim)
		}
		metaJSON, _ := json.Marshal(doc.Metadata)
		metaStrs[i] = string(metaJSON)
	}

	opt := client.NewColumnBasedInsertOption(collectionName).
		WithVarcharColumn("id", ids).
		WithInt64Column("kb_id", kbIDs).
		WithInt64Column("doc_id", docIDs).
		WithInt32Column("chunk_index", chunkIdx).
		WithVarcharColumn("content", contents).
		WithFloatVectorColumn(denseField, defaultDim, denseVecs).
		WithVarcharColumn("chunk_meta", metaStrs).
		WithInt64Column("created_at", createdAts)

	_, err := s.cli.Upsert(ctx, opt)
	if err != nil {
		return fmt.Errorf("milvus upsert: %w", err)
	}

	_, err = s.cli.Flush(ctx, client.NewFlushOption(collectionName))
	if err != nil {
		return fmt.Errorf("milvus flush: %w", err)
	}

	loadTask, err := s.cli.LoadCollection(ctx, client.NewLoadCollectionOption(collectionName))
	if err != nil {
		return err
	}
	return loadTask.Await(ctx)
}

func (s *MilvusStore) Search(ctx context.Context, vector []float32, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure milvus collection: %w", err)
	}

	expr := buildFilterExpr(filter)
	searchOpt := client.NewSearchOption(collectionName, topK, []entity.Vector{entity.FloatVector(vector)}).
		WithANNSField(denseField).
		WithOutputFields("*")
	if expr != "" {
		searchOpt.WithFilter(expr)
	}

	resultSet, err := s.cli.Search(ctx, searchOpt)
	if err != nil {
		return nil, fmt.Errorf("milvus search: %w", err)
	}

	return convertSearchResults(resultSet, filter.ScoreThreshold), nil
}

func (s *MilvusStore) SparseSearch(ctx context.Context, query string, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure milvus collection: %w", err)
	}
	if query == "" {
		return nil, nil
	}

	expr := buildFilterExpr(filter)
	searchOpt := client.NewSearchOption(collectionName, topK, []entity.Vector{entity.Text(query)}).
		WithANNSField(sparseField).
		WithOutputFields("*")
	if expr != "" {
		searchOpt.WithFilter(expr)
	}

	resultSet, err := s.cli.Search(ctx, searchOpt)
	if err != nil {
		return nil, fmt.Errorf("milvus sparse search: %w", err)
	}

	return convertSearchResults(resultSet, filter.ScoreThreshold), nil
}

func (s *MilvusStore) HybridSearch(ctx context.Context, vector []float32, query string, topK int,
	filter domain.SearchFilter, weight domain.WeightConfig,
) ([]domain.SearchResult, error) {
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure milvus collection: %w", err)
	}

	expr := buildFilterExpr(filter)

	// Dense ANN request
	denseReq := client.NewAnnRequest(denseField, topK, entity.FloatVector(vector))
	if expr != "" {
		denseReq.WithFilter(expr)
	}

	// Sparse ANN request (BM25) — query is raw text, Milvus computes BM25 server-side
	annReqs := []*client.AnnRequest{denseReq}
	if query != "" {
		sparseReq := client.NewAnnRequest(sparseField, topK, entity.Text(query))
		if expr != "" {
			sparseReq.WithFilter(expr)
		}
		annReqs = append(annReqs, sparseReq)
	}

	// Build hybrid search with RRF reranker (reciprocal rank fusion)
	hybridOpt := client.NewHybridSearchOption(collectionName, topK, annReqs...).
		WithReranker(client.NewRRFReranker()).
		WithOutputFields("*")

	resultSet, err := s.cli.HybridSearch(ctx, hybridOpt)
	if err != nil {
		return nil, fmt.Errorf("milvus hybrid search: %w", err)
	}

	return convertSearchResults(resultSet, filter.ScoreThreshold), nil
}

func (s *MilvusStore) DeleteByKB(ctx context.Context, kbID int64) error {
	expr := fmt.Sprintf("kb_id == %d", kbID)
	_, err := s.cli.Delete(ctx, client.NewDeleteOption(collectionName).WithExpr(expr))
	return err
}

func (s *MilvusStore) DeleteByDoc(ctx context.Context, docID int64) error {
	expr := fmt.Sprintf("doc_id == %d", docID)
	_, err := s.cli.Delete(ctx, client.NewDeleteOption(collectionName).WithExpr(expr))
	return err
}

func (s *MilvusStore) GetByIDs(ctx context.Context, ids []string) ([]domain.SearchResult, error) {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf(`"%s"`, id)
	}
	expr := fmt.Sprintf("id in [%s]", strings.Join(quoted, ","))
	queryOpt := client.NewQueryOption(collectionName).WithFilter(expr).WithOutputFields("*")
	resultSet, err := s.cli.Query(ctx, queryOpt)
	if err != nil {
		return nil, fmt.Errorf("query by ids failed: %w", err)
	}
	return fromResultSet(resultSet), nil
}

func (s *MilvusStore) Close(ctx context.Context) error {
	return s.cli.Close(ctx)
}

func buildFilterExpr(filter domain.SearchFilter) string {
	if len(filter.KBIDs) == 0 {
		return ""
	}
	ids := make([]string, len(filter.KBIDs))
	for i, id := range filter.KBIDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	return fmt.Sprintf("kb_id in [%s]", strings.Join(ids, ","))
}

func getColumnString(col column.Column, idx int) (string, error) {
	if col == nil || col.Len() <= idx {
		return "", fmt.Errorf("index out of range")
	}
	return col.GetAsString(idx)
}

func convertSearchResults(resultSet []client.ResultSet, threshold float32) []domain.SearchResult {
	var out []domain.SearchResult
	if len(resultSet) == 0 {
		return out
	}
	set := resultSet[0]
	for i := 0; i < set.Len(); i++ {
		score := float32(0)
		if i < len(set.Scores) {
			score = float32(set.Scores[i])
		}
		if score < threshold {
			continue
		}
		id, _ := getColumnString(set.GetColumn("id"), i)
		content, _ := getColumnString(set.GetColumn("content"), i)
		var kbID int64
		if col := set.GetColumn("kb_id"); col != nil && col.Len() > i {
			if v, err := col.Get(i); err == nil {
				kbID, _ = v.(int64)
			}
		}
		var meta map[string]any
		if raw, err := getColumnString(set.GetColumn("chunk_meta"), i); err == nil && raw != "" {
			json.Unmarshal([]byte(raw), &meta)
		}
		out = append(out, domain.SearchResult{
			DocID:    id,
			Score:    score,
			Content:  content,
			KBID:     kbID,
			Metadata: meta,
		})
	}
	return out
}

func fromResultSet(set client.ResultSet) []domain.SearchResult {
	var out []domain.SearchResult
	for i := 0; i < set.Len(); i++ {
		id, _ := getColumnString(set.GetColumn("id"), i)
		content, _ := getColumnString(set.GetColumn("content"), i)
		if id == "" {
			continue
		}
		var kbID int64
		if col := set.GetColumn("kb_id"); col != nil && col.Len() > i {
			if v, err := col.Get(i); err == nil {
				kbID, _ = v.(int64)
			}
		}
		var meta map[string]any
		if raw, err := getColumnString(set.GetColumn("chunk_meta"), i); err == nil && raw != "" {
			json.Unmarshal([]byte(raw), &meta)
		}
		out = append(out, domain.SearchResult{
			DocID:    id,
			Content:  content,
			KBID:     kbID,
			Metadata: meta,
		})
	}
	return out
}
