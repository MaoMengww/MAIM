package domain

import (
	"testing"

	"github.com/maomeng/aim/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      *errors.BizError
		wantCode int
		wantMsg  string
	}{
		{"ErrKBNotFound", ErrKBNotFound, errors.CodeNotFound, "knowledge base not found"},
		{"ErrDocNotFound", ErrDocNotFound, errors.CodeNotFound, "document not found"},
		{"ErrForbidden", ErrForbidden, errors.CodeForbidden, "permission denied"},
		{"ErrInvalidFileType", ErrInvalidFileType, errors.CodeInvalidParam, "invalid file type"},
		{"ErrFileTooLarge", ErrFileTooLarge, errors.CodeInvalidParam, "file too large"},
		{"ErrDocumentNotFailed", ErrDocumentNotFailed, errors.CodeInvalidParam, "document is not in failed status"},
		{"ErrDuplicateBinding", ErrDuplicateBinding, errors.CodeConflict, "binding already exists"},
		{"ErrParserUnsupported", ErrParserUnsupported, errors.CodeInvalidParam, "parser cannot handle this document"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantCode, tt.err.Code)
			assert.Equal(t, tt.wantMsg, tt.err.Message)
		})
	}
}

func TestDocStatusConstants(t *testing.T) {
	tests := []struct {
		name   string
		status DocStatus
	}{
		{"pending", DocStatusPending},
		{"parsing", DocStatusParsing},
		{"chunking", DocStatusChunking},
		{"embedding", DocStatusEmbedding},
		{"ready", DocStatusReady},
		{"failed", DocStatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.name, string(tt.status))
		})
	}
}

func TestChunkIDFormat(t *testing.T) {
	c := &Chunk{
		KBID:  1,
		DocID: 42,
		Index: 7,
	}
	assert.Equal(t, "kb_1_doc_42_chk_7", c.ID())
}

func TestKnowledgeBaseTableName(t *testing.T) {
	assert.Equal(t, "knowledge_bases", (&KnowledgeBase{}).TableName())
}

func TestDocumentTableName(t *testing.T) {
	assert.Equal(t, "documents", (&Document{}).TableName())
}

func TestChunkRecordTableName(t *testing.T) {
	assert.Equal(t, "document_chunks", (&ChunkRecord{}).TableName())
}

func TestKnowledgeBindingTableName(t *testing.T) {
	assert.Equal(t, "knowledge_bindings", (&KnowledgeBinding{}).TableName())
}

func TestRetrieveItemFields(t *testing.T) {
	item := RetrieveItem{
		ChunkID:        1,
		Content:        "hello world",
		Score:          0.95,
		DocID:          10,
		DocTitle:       "test doc",
		KBID:           100,
		KBName:         "test kb",
		MatchedContent: "hello",
		Metadata:       map[string]any{"page": 1},
	}
	assert.Equal(t, int64(1), item.ChunkID)
	assert.Equal(t, "hello world", item.Content)
	assert.Equal(t, float32(0.95), item.Score)
	assert.Equal(t, "hello", item.MatchedContent)
}

func TestPipelineConfigDefaults(t *testing.T) {
	cfg := PipelineConfig{}
	assert.Empty(t, cfg.Parsing.Engines)
	assert.Zero(t, cfg.Chunking.ChunkSize)
	assert.False(t, cfg.Retrieval.Rerank.Enabled)
}
