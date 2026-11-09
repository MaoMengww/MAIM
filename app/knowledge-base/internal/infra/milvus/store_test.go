package milvus

import (
	"encoding/json"
	"testing"

	"github.com/milvus-io/milvus/client/v2/entity"
)

func TestChunkCollectionUsesChineseAnalyzer(t *testing.T) {
	schema := chunkCollectionSchema()

	var content *entity.Field
	for _, field := range schema.Fields {
		if field.Name == "content" {
			content = field
		}
	}
	if content == nil {
		t.Fatal("content field is missing")
	}
	if content.TypeParams["enable_analyzer"] != "true" {
		t.Fatalf("content analyzer is not enabled: %v", content.TypeParams)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(content.TypeParams["analyzer_params"]), &params); err != nil {
		t.Fatalf("analyzer params are not JSON: %v", err)
	}
	if params["type"] != "chinese" {
		t.Fatalf("content analyzer must segment Chinese: %v", params)
	}
	if content.TypeParams["enable_match"] != "true" {
		t.Fatalf("content match is not enabled: %v", content.TypeParams)
	}

	var bm25 *entity.Function
	for _, fn := range schema.Functions {
		if fn.Name == "bm25_fn" {
			bm25 = fn
		}
	}
	if bm25 == nil || bm25.Type != entity.FunctionTypeBM25 {
		t.Fatalf("bm25 function is missing: %+v", schema.Functions)
	}
	if len(bm25.InputFieldNames) != 1 || bm25.InputFieldNames[0] != "content" {
		t.Fatalf("bm25 must read content: %v", bm25.InputFieldNames)
	}
	if len(bm25.OutputFieldNames) != 1 || bm25.OutputFieldNames[0] != sparseField {
		t.Fatalf("bm25 must write %s: %v", sparseField, bm25.OutputFieldNames)
	}
}
