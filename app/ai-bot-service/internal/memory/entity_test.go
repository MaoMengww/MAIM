package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryItem_TableName(t *testing.T) {
	m := MemoryItem{}
	assert.Equal(t, "bot_memories", m.TableName())
}

func TestStore_Interface(t *testing.T) {
	// Compile-time check: *PgRepo satisfies Store
	var _ Store = (*PgRepo)(nil)
}

func TestExtractedFact_Defaults(t *testing.T) {
	fact := ExtractedFact{
		Content:    "用户在北京工作",
		Importance: 0.5,
	}
	assert.Equal(t, "用户在北京工作", fact.Content)
	assert.Equal(t, 0.5, fact.Importance)
}

func TestExtractResult_Empty(t *testing.T) {
	result := &ExtractResult{}
	assert.Empty(t, result.Facts)
}

func TestParseExtractionResult_Valid(t *testing.T) {
	raw := `{"facts":[{"content":"喜欢打篮球","category":"爱好","importance":0.9,"confidence":0.95}]}`
	result, err := ParseExtractionResult(raw)
	require.NoError(t, err)
	require.Len(t, result.Facts, 1)
	assert.Equal(t, "喜欢打篮球", result.Facts[0].Content)
	assert.Equal(t, "爱好", result.Facts[0].Category)
	assert.Equal(t, 0.9, result.Facts[0].Importance)
}

func TestParseExtractionResult_EmptyFacts(t *testing.T) {
	raw := `{"facts":[]}`
	result, err := ParseExtractionResult(raw)
	require.NoError(t, err)
	assert.Empty(t, result.Facts)
}

func TestParseExtractionResult_InvalidJSON(t *testing.T) {
	_, err := ParseExtractionResult(`not json`)
	assert.Error(t, err)
}

func TestFormatDialog(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "今天天气怎么样"},
		{Role: "assistant", Content: "今天晴天，25°C"},
	}
	result := formatDialog(msgs)
	assert.Contains(t, result, "user: 今天天气怎么样")
	assert.Contains(t, result, "assistant: 今天晴天，25°C")
}

func TestFormatDialog_Empty(t *testing.T) {
	result := formatDialog(nil)
	assert.Empty(t, result)
}
