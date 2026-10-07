package milvus

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"

	"github.com/maomeng/aim/app/knowledge-base/internal/config"
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/identity"
)

const (
	collectionName = "kb_chunks_uuid_v1"
	denseField     = "dense_vector"
	sparseField    = "sparse_vector"
	defaultDim     = 1536
)

type MilvusStore struct {
	cli             *client.Client
	collectionMu    sync.Mutex
	collectionReady bool
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
	s.collectionMu.Lock()
	defer s.collectionMu.Unlock()
	if s.collectionReady {
		return nil
	}

	has, err := s.cli.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil {
		return err
	}
	if !has {
		schema := &entity.Schema{
			CollectionName: collectionName,
			Description:    "AIM knowledge chunks with shared relational UUID identity",
			AutoID:         false,
			Fields: []*entity.Field{
				entity.NewField().WithName("id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36).WithIsPrimaryKey(true),
				entity.NewField().WithName("kb_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36),
				entity.NewField().WithName("doc_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36),
				entity.NewField().WithName("chunk_index").WithDataType(entity.FieldTypeInt32),
				entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).WithMaxLength(65535).WithEnableAnalyzer(true).WithEnableMatch(true),
				entity.NewField().WithName(denseField).WithDataType(entity.FieldTypeFloatVector).WithDim(defaultDim),
				entity.NewField().WithName(sparseField).WithDataType(entity.FieldTypeSparseVector),
				entity.NewField().WithName("chunk_meta").WithDataType(entity.FieldTypeVarChar).WithMaxLength(65535),
				entity.NewField().WithName("created_at").WithDataType(entity.FieldTypeInt64),
			},
		}
		schema.WithFunction(entity.NewFunction().
			WithName("bm25_fn").
			WithType(entity.FunctionTypeBM25).
			WithInputFields("content").
			WithOutputFields(sparseField),
		)
		createOpt := client.NewCreateCollectionOption(collectionName, schema).
			WithConsistencyLevel(entity.ClStrong).
			WithIndexOptions(
				client.NewCreateIndexOption(collectionName, denseField, index.NewHNSWIndex(entity.COSINE, 16, 200)),
				client.NewCreateIndexOption(collectionName, sparseField, index.NewSparseInvertedIndex(entity.BM25, 0.3)),
			)
		if err := s.cli.CreateCollection(ctx, createOpt); err != nil {
			// Online retrieval and ingestion can initialize the same collection
			// in different processes. Only an observed collection resolves the race.
			exists, checkErr := s.cli.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
			if checkErr != nil || !exists {
				return fmt.Errorf("create collection failed: %w", err)
			}
		}
	}
	if err := s.ensureIndexes(ctx); err != nil {
		return err
	}
	loadTask, err := s.cli.LoadCollection(ctx, client.NewLoadCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("load collection failed: %w", err)
	}
	if err := loadTask.Await(ctx); err != nil {
		return err
	}
	s.collectionReady = true
	return nil
}

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
	for fieldName, opt := range required {
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
	ids := make([]string, len(docs))
	kbIDs := make([]string, len(docs))
	docIDs := make([]string, len(docs))
	chunkIdx := make([]int32, len(docs))
	contents := make([]string, len(docs))
	denseVecs := make([][]float32, len(docs))
	metaStrs := make([]string, len(docs))
	createdAts := make([]int64, len(docs))
	positions := make(map[string]int, len(docs))
	n := 0
	for _, doc := range docs {
		for _, id := range []string{doc.ChunkID, doc.DocID, doc.KBID} {
			if err := identity.Validate(id); err != nil {
				return err
			}
		}
		if err := validateVector(doc.Vector); err != nil {
			return fmt.Errorf("chunk %s: %w", doc.ChunkID, err)
		}
		if parent, exists := doc.Metadata["parent_chunk_id"]; exists {
			parentID, ok := parent.(string)
			if !ok {
				return fmt.Errorf("parent_chunk_id must be a UUID string")
			}
			if err := identity.Validate(parentID); err != nil {
				return err
			}
		}
		metaJSON, err := json.Marshal(doc.Metadata)
		if err != nil {
			return fmt.Errorf("encode chunk metadata: %w", err)
		}
		i, exists := positions[doc.ChunkID]
		if !exists {
			i = n
			n++
			positions[doc.ChunkID] = i
		} else if docIDs[i] != doc.DocID || kbIDs[i] != doc.KBID {
			return fmt.Errorf("chunk %s has conflicting document identity", doc.ChunkID)
		}
		ids[i] = doc.ChunkID
		kbIDs[i] = doc.KBID
		docIDs[i] = doc.DocID
		contents[i] = doc.Content
		denseVecs[i] = doc.Vector
		metaStrs[i] = string(metaJSON)
		chunkIdx[i] = 0
		switch v := doc.Metadata["chunk_index"].(type) {
		case int:
			if v < 0 || int64(v) > math.MaxInt32 {
				return fmt.Errorf("chunk index is out of range")
			}
			chunkIdx[i] = int32(v)
		case int32:
			if v < 0 {
				return fmt.Errorf("chunk index is out of range")
			}
			chunkIdx[i] = v
		case int64:
			if v < 0 || v > math.MaxInt32 {
				return fmt.Errorf("chunk index is out of range")
			}
			chunkIdx[i] = int32(v)
		}
	}
	if err := s.ensureCollection(ctx); err != nil {
		return fmt.Errorf("ensure milvus collection: %w", err)
	}
	opt := client.NewColumnBasedInsertOption(collectionName).
		WithVarcharColumn("id", ids[:n]).
		WithVarcharColumn("kb_id", kbIDs[:n]).
		WithVarcharColumn("doc_id", docIDs[:n]).
		WithInt32Column("chunk_index", chunkIdx[:n]).
		WithVarcharColumn("content", contents[:n]).
		WithFloatVectorColumn(denseField, defaultDim, denseVecs[:n]).
		WithVarcharColumn("chunk_meta", metaStrs[:n]).
		WithInt64Column("created_at", createdAts[:n])
	if _, err := s.cli.Upsert(ctx, opt); err != nil {
		return fmt.Errorf("milvus upsert: %w", err)
	}
	// Strong reads wait for the acknowledged mutation; sealing every document
	// is unnecessary and would turn the collection's Flush budget into ingest failures.
	return nil
}

func (s *MilvusStore) Search(ctx context.Context, vector []float32, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	if err := validateVector(vector); err != nil {
		return nil, err
	}
	expr, err := buildFilterExpr(filter)
	if err != nil {
		return nil, err
	}
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure milvus collection: %w", err)
	}
	searchOpt := client.NewSearchOption(collectionName, topK, []entity.Vector{entity.FloatVector(vector)}).
		WithANNSField(denseField).
		WithConsistencyLevel(entity.ClStrong).
		WithOutputFields("id", "kb_id", "doc_id", "content", "chunk_meta")
	if expr != "" {
		searchOpt.WithFilter(expr)
	}
	resultSet, err := s.cli.Search(ctx, searchOpt)
	if err != nil {
		return nil, fmt.Errorf("milvus search: %w", err)
	}
	return convertSearchResults(resultSet, filter.ScoreThreshold)
}

func (s *MilvusStore) SparseSearch(ctx context.Context, query string, topK int, filter domain.SearchFilter) ([]domain.SearchResult, error) {
	expr, err := buildFilterExpr(filter)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return nil, nil
	}
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure milvus collection: %w", err)
	}
	searchOpt := client.NewSearchOption(collectionName, topK, []entity.Vector{entity.Text(query)}).
		WithANNSField(sparseField).
		WithConsistencyLevel(entity.ClStrong).
		WithOutputFields("id", "kb_id", "doc_id", "content", "chunk_meta")
	if expr != "" {
		searchOpt.WithFilter(expr)
	}
	resultSet, err := s.cli.Search(ctx, searchOpt)
	if err != nil {
		return nil, fmt.Errorf("milvus sparse search: %w", err)
	}
	return convertSearchResults(resultSet, filter.ScoreThreshold)
}

func (s *MilvusStore) HybridSearch(ctx context.Context, vector []float32, query string, topK int,
	filter domain.SearchFilter, weight domain.WeightConfig,
) ([]domain.SearchResult, error) {
	if err := validateVector(vector); err != nil {
		return nil, err
	}
	expr, err := buildFilterExpr(filter)
	if err != nil {
		return nil, err
	}
	if err := s.ensureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure milvus collection: %w", err)
	}
	denseReq := client.NewAnnRequest(denseField, topK, entity.FloatVector(vector))
	if expr != "" {
		denseReq.WithFilter(expr)
	}
	annReqs := []*client.AnnRequest{denseReq}
	if query != "" {
		sparseReq := client.NewAnnRequest(sparseField, topK, entity.Text(query))
		if expr != "" {
			sparseReq.WithFilter(expr)
		}
		annReqs = append(annReqs, sparseReq)
	}
	hybridOpt := client.NewHybridSearchOption(collectionName, topK, annReqs...).
		WithReranker(client.NewRRFReranker()).
		WithConsistencyLevel(entity.ClStrong).
		WithOutputFields("id", "kb_id", "doc_id", "content", "chunk_meta")
	resultSet, err := s.cli.HybridSearch(ctx, hybridOpt)
	if err != nil {
		return nil, fmt.Errorf("milvus hybrid search: %w", err)
	}
	return convertSearchResults(resultSet, filter.ScoreThreshold)
}

func (s *MilvusStore) DeleteByKB(ctx context.Context, kbID string) error {
	return s.deleteByReference(ctx, "kb_id", kbID)
}

func (s *MilvusStore) DeleteByDoc(ctx context.Context, docID string) error {
	return s.deleteByReference(ctx, "doc_id", docID)
}

func (s *MilvusStore) deleteByReference(ctx context.Context, field, id string) error {
	if err := identity.Validate(id); err != nil {
		return err
	}
	has, err := s.cli.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil || !has {
		return err
	}
	expr := field + " == " + strconv.Quote(id)
	if _, err := s.cli.Delete(ctx, client.NewDeleteOption(collectionName).WithExpr(expr)); err != nil {
		return fmt.Errorf("milvus delete: %w", err)
	}
	return nil
}

func (s *MilvusStore) GetByIDs(ctx context.Context, ids []string) ([]domain.SearchResult, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	expr, err := uuidListExpr("id", ids)
	if err != nil {
		return nil, err
	}
	has, err := s.cli.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil || !has {
		return nil, err
	}
	if err := s.ensureCollection(ctx); err != nil {
		return nil, err
	}
	queryOpt := client.NewQueryOption(collectionName).
		WithFilter(expr).
		WithConsistencyLevel(entity.ClStrong).
		WithOutputFields("id", "kb_id", "doc_id", "content", "chunk_meta")
	resultSet, err := s.cli.Query(ctx, queryOpt)
	if err != nil {
		return nil, fmt.Errorf("query by ids failed: %w", err)
	}
	return fromResultSet(resultSet)
}

func (s *MilvusStore) Close(ctx context.Context) error {
	return s.cli.Close(ctx)
}

func validateVector(vector []float32) error {
	if len(vector) != defaultDim {
		return fmt.Errorf("embedding vector dimension must be %d, got %d", defaultDim, len(vector))
	}
	nonzero := false
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("embedding vector must contain only finite values")
		}
		nonzero = nonzero || value != 0
	}
	if !nonzero {
		return fmt.Errorf("embedding vector must not be zero")
	}
	return nil
}

func buildFilterExpr(filter domain.SearchFilter) (string, error) {
	if len(filter.KBIDs) == 0 {
		return "", nil
	}
	return uuidListExpr("kb_id", filter.KBIDs)
}

func uuidListExpr(field string, ids []string) (string, error) {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		if err := identity.Validate(id); err != nil {
			return "", err
		}
		quoted[i] = strconv.Quote(id)
	}
	return field + " in [" + strings.Join(quoted, ",") + "]", nil
}

func getColumnString(col column.Column, idx int) (string, error) {
	if col == nil || idx < 0 || col.Len() <= idx {
		return "", fmt.Errorf("missing result column or index out of range")
	}
	return col.GetAsString(idx)
}

func readResult(set client.ResultSet, i int) (domain.SearchResult, error) {
	idColumn := set.IDs
	if idColumn == nil {
		idColumn = set.GetColumn("id")
	}
	chunkID, err := getColumnString(idColumn, i)
	if err != nil {
		return domain.SearchResult{}, err
	}
	docID, err := getColumnString(set.GetColumn("doc_id"), i)
	if err != nil {
		return domain.SearchResult{}, err
	}
	kbID, err := getColumnString(set.GetColumn("kb_id"), i)
	if err != nil {
		return domain.SearchResult{}, err
	}
	for _, id := range []string{chunkID, docID, kbID} {
		if err := identity.Validate(id); err != nil {
			return domain.SearchResult{}, err
		}
	}
	content, err := getColumnString(set.GetColumn("content"), i)
	if err != nil {
		return domain.SearchResult{}, err
	}
	metaRaw, err := getColumnString(set.GetColumn("chunk_meta"), i)
	if err != nil {
		return domain.SearchResult{}, err
	}
	var metadata map[string]any
	if metaRaw != "" {
		if err := json.Unmarshal([]byte(metaRaw), &metadata); err != nil {
			return domain.SearchResult{}, fmt.Errorf("decode chunk metadata: %w", err)
		}
	}
	return domain.SearchResult{
		ChunkID:  chunkID,
		DocID:    docID,
		Content:  content,
		KBID:     kbID,
		Metadata: metadata,
	}, nil
}

func convertSearchResults(resultSet []client.ResultSet, threshold float32) ([]domain.SearchResult, error) {
	var out []domain.SearchResult
	for _, set := range resultSet {
		if set.Err != nil {
			return nil, set.Err
		}
		for i := range set.Len() {
			if i >= len(set.Scores) {
				return nil, fmt.Errorf("search result is missing score")
			}
			if set.Scores[i] < threshold {
				continue
			}
			result, err := readResult(set, i)
			if err != nil {
				return nil, err
			}
			result.Score = set.Scores[i]
			out = append(out, result)
		}
	}
	return out, nil
}

func fromResultSet(set client.ResultSet) ([]domain.SearchResult, error) {
	if set.Err != nil {
		return nil, set.Err
	}
	out := make([]domain.SearchResult, 0, set.Len())
	for i := range set.Len() {
		result, err := readResult(set, i)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, nil
}
