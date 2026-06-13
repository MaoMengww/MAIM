package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestBuiltinParser_Name(t *testing.T) {
	p := NewBuiltinParser("txt")
	assert.Equal(t, "builtin", p.Name())
}

func TestBuiltinParser_ParseMarkdown(t *testing.T) {
	p := NewBuiltinParser("txt")
	text := `# Title

## Section 1
Content of section 1.

## Section 2
Content of section 2.`

	doc, err := p.Parse(context.Background(), []byte(text))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, text, doc.RawText)
	assert.Len(t, doc.Pages, 1)
	assert.GreaterOrEqual(t, len(doc.Sections), 1)
}

func TestBuiltinParser_ParsePlainText(t *testing.T) {
	p := NewBuiltinParser("txt")
	text := "This is a plain text document without any headings.\nJust some content."

	doc, err := p.Parse(context.Background(), []byte(text))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, text, doc.RawText)
	assert.NotEmpty(t, doc.Sections)
}

func TestBuiltinParser_ParseEmptyText(t *testing.T) {
	p := NewBuiltinParser("txt")
	doc, err := p.Parse(context.Background(), []byte(""))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Empty(t, doc.RawText)
}

func TestBuiltinParser_DetectsLanguage(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantLang string
	}{
		{"english", "Hello world! This is a test.", "en"},
		{"chinese", "你好世界！这是一个测试文档。", "zh"},
		{"empty", "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewBuiltinParser("txt")
			doc, err := p.Parse(context.Background(), []byte(tt.text))
			assert.NoError(t, err)
			assert.Equal(t, tt.wantLang, doc.Metadata.Language)
		})
	}
}

func TestBuiltinParser_DetectsHeadings(t *testing.T) {
	p := NewBuiltinParser("txt")
	text := "# H1\n## H2\n### H3\n#### H4\nPlain text."

	doc, err := p.Parse(context.Background(), []byte(text))
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(doc.Sections), 3)
	assert.Equal(t, "H1", doc.Sections[0].Title)
	assert.Equal(t, 1, doc.Sections[0].Level)
	assert.Equal(t, "H2", doc.Sections[1].Title)
	assert.Equal(t, 2, doc.Sections[1].Level)
	assert.Contains(t, doc.Sections[len(doc.Sections)-1].Content, "Plain text.")
}

func TestBuiltinParser_ParseText(t *testing.T) {
	p := NewBuiltinParser("txt")
	doc, err := p.Parse(context.Background(), []byte("hello world"))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
}

func TestParserChain_FirstSuccess(t *testing.T) {
	callOrder := []string{}

	p1 := &recordParser{name: "p1", result: &domain.ParsedDocument{RawText: "ok"}, record: &callOrder}
	p2 := &recordParser{name: "p2", err: errors.New("should not be called"), record: &callOrder}

	chain := NewParserChain(p1, p2)
	doc, err := chain.Parse(context.Background(), []byte("test"))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, "ok", doc.RawText)
	assert.Len(t, callOrder, 1)
	assert.Equal(t, "p1", callOrder[0])
}

func TestParserChain_FallthroughOnErrUnsupported(t *testing.T) {
	callOrder := []string{}

	p1 := &recordParser{name: "p1", err: domain.ErrParserUnsupported, record: &callOrder}
	p2 := &recordParser{name: "p2", result: &domain.ParsedDocument{RawText: "fallback"}, record: &callOrder}

	chain := NewParserChain(p1, p2)
	doc, err := chain.Parse(context.Background(), []byte("test"))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, "fallback", doc.RawText)
	assert.Len(t, callOrder, 2)
}

func TestParserChain_AllFail(t *testing.T) {
	p1 := &recordParser{name: "p1", err: domain.ErrParserUnsupported, record: &[]string{}}
	p2 := &recordParser{name: "p2", err: domain.ErrParserUnsupported, record: &[]string{}}

	chain := NewParserChain(p1, p2)
	doc, err := chain.Parse(context.Background(), []byte("test"))
	assert.Error(t, err)
	assert.Nil(t, doc)
}

func TestParserChain_StopsOnRealError(t *testing.T) {
	callOrder := []string{}
	fatalErr := errors.New("fatal")

	p1 := &recordParser{name: "p1", err: fatalErr, record: &callOrder}
	p2 := &recordParser{name: "p2", result: &domain.ParsedDocument{RawText: "ok"}, record: &callOrder}

	chain := NewParserChain(p1, p2)
	doc, err := chain.Parse(context.Background(), []byte("test"))
	assert.Error(t, err)
	assert.Nil(t, doc)
	assert.Len(t, callOrder, 1)
}

func TestSetupParser_Engines(t *testing.T) {
	tests := []struct {
		name     string
		engines  []string
		fileType string
		wantNil  bool
	}{
		{"builtin supports txt", []string{"builtin"}, "txt", false},
		{"builtin supports md", []string{"builtin"}, "md", false},
		{"builtin does not support pdf", []string{"builtin"}, "pdf", true},
		{"mineru_precision supports pdf with URL", []string{"mineru_precision"}, "pdf", false},
		{"mineru_precision no URL", []string{"mineru_precision"}, "pdf", true},
		{"no engine supports unknown type", []string{"builtin"}, "exe", true},
		{"empty engines", []string{}, "txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := domain.ParsingConfig{Engines: tt.engines}
			if tt.name == "mineru_precision supports pdf with URL" {
				cfg.MinerUPrecision = &domain.MinerUConfig{APIURL: "http://mineru:8080"}
			}
			p := SetupParser(cfg, tt.fileType)
			if tt.wantNil {
				assert.Nil(t, p)
			} else {
				assert.NotNil(t, p)
			}
		})
	}
}

func TestMinerUParser_EmptyURL(t *testing.T) {
	p := NewMinerUParser("", "")
	doc, err := p.Parse(context.Background(), []byte("test"))
	assert.Error(t, err)
	assert.Nil(t, doc)
}

func TestMinerUParser_Name(t *testing.T) {
	p := NewMinerUParser("http://mineru:8080", "")
	assert.Equal(t, "mineru_precision", p.Name())
}

func TestIsBinary_NullBytes(t *testing.T) {
	assert.True(t, isBinary([]byte{0x00, 0x01, 0x02}))
	assert.False(t, isBinary([]byte("hello")))
}

func TestSupportsFileType(t *testing.T) {
	assert.True(t, supportsFileType("builtin", "txt"))
	assert.True(t, supportsFileType("builtin", "md"))
	assert.True(t, supportsFileType("mineru_precision", "pdf"))
	assert.True(t, supportsFileType("mineru_precision", "docx"))
	assert.True(t, supportsFileType("mineru", "pdf"))
	assert.True(t, supportsFileType("mineru", "png"))
	assert.True(t, supportsFileType("mineru", "xls"))
	assert.False(t, supportsFileType("builtin", "pdf"))
	assert.False(t, supportsFileType("unknown", "txt"))
}

// ---------------------------------------------------------------------------
// Format-specific converter tests
// ---------------------------------------------------------------------------

func TestBuiltinParser_ParseHTML(t *testing.T) {
	p := NewBuiltinParser("html")
	html := `<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body>
	<h1>Title</h1>
	<p>First paragraph.</p>
	<p>Second <strong>paragraph</strong>.</p>
	<script>alert('xss')</script>
	<style>body { color: red; }</style>
</body>
</html>`

	doc, err := p.Parse(context.Background(), []byte(html))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Script and style content should be stripped
	assert.NotContains(t, doc.RawText, "alert('xss')")
	assert.NotContains(t, doc.RawText, "color: red")
	// Text content should be present
	assert.Contains(t, doc.RawText, "First paragraph")
	assert.Contains(t, doc.RawText, "Second paragraph")
	// H1 should be converted to markdown heading
	assert.Contains(t, doc.RawText, "# Title")
}

func TestBuiltinParser_ParseJSON(t *testing.T) {
	p := NewBuiltinParser("json")
	jsonInput := `{"name": "John", "age": 30, "city": "NYC"}`

	doc, err := p.Parse(context.Background(), []byte(jsonInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Should be wrapped in ```json code block
	assert.Contains(t, doc.RawText, "```json")
	assert.Contains(t, doc.RawText, "John")
	assert.Contains(t, doc.RawText, "NYC")
}

func TestBuiltinParser_ParseLargeJSON(t *testing.T) {
	p := NewBuiltinParser("json")
	// Create a JSON object large enough to trigger splitting (>1536 bytes)
	var largeData []map[string]any
	for i := 0; i < 200; i++ {
		largeData = append(largeData, map[string]any{
			"id":          i,
			"name":        fmt.Sprintf("item-%d", i),
			"description": "This is a long description to fill up space quickly and trigger the recursive JSON splitting logic",
		})
	}
	raw, _ := json.Marshal(map[string]any{"data": largeData})
	assert.True(t, len(raw) > jsonChunkSize, "test data must exceed chunk size")

	doc, err := p.Parse(context.Background(), raw)
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Should produce multiple ```json blocks
	count := strings.Count(doc.RawText, "```json")
	assert.GreaterOrEqual(t, count, 2, "large JSON should produce multiple blocks, got %d", count)
	// Each block should be valid JSON
	assertValidJSONBlocks(t, doc.RawText)
}

func TestBuiltinParser_ParseJSONArray(t *testing.T) {
	p := NewBuiltinParser("json")
	jsonInput := `["apple", "banana", "cherry"]`

	doc, err := p.Parse(context.Background(), []byte(jsonInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Array should be converted to indexed object
	assert.Contains(t, doc.RawText, "```json")
	assert.Contains(t, doc.RawText, "apple")
}

func TestBuiltinParser_ParseInvalidJSON(t *testing.T) {
	p := NewBuiltinParser("json")
	notJSON := `This is not valid JSON {broken`

	doc, err := p.Parse(context.Background(), []byte(notJSON))
	assert.NoError(t, err)
	// Invalid JSON should be returned as-is
	assert.Equal(t, notJSON, doc.RawText)
}

func TestBuiltinParser_ParseEmptyJSON(t *testing.T) {
	p := NewBuiltinParser("json")

	doc, err := p.Parse(context.Background(), []byte("{}"))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Contains(t, doc.RawText, "```json")
}

func TestBuiltinParser_ParseCSV(t *testing.T) {
	p := NewBuiltinParser("csv")
	csvInput := "Name,Age,City\nJohn,30,NYC\nJane,25,SF"

	doc, err := p.Parse(context.Background(), []byte(csvInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Should be a markdown table
	assert.Contains(t, doc.RawText, "| Name | Age | City |")
	assert.Contains(t, doc.RawText, "| --- | --- | --- |")
	assert.Contains(t, doc.RawText, "| John | 30 | NYC |")
	assert.Contains(t, doc.RawText, "| Jane | 25 | SF |")
}

func TestBuiltinParser_ParseCSVWithQuotes(t *testing.T) {
	p := NewBuiltinParser("csv")
	csvInput := "Name,Description\n" + `"Doe, John","A long, quoted description"`

	doc, err := p.Parse(context.Background(), []byte(csvInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Quoted fields should be handled, commas preserved inside quotes
	assert.Contains(t, doc.RawText, "Doe, John")
	assert.Contains(t, doc.RawText, "A long, quoted description")
}

func TestBuiltinParser_ParseCSVUnevenColumns(t *testing.T) {
	p := NewBuiltinParser("csv")
	csvInput := "A,B,C\n1,2\n3,4,5,6"

	doc, err := p.Parse(context.Background(), []byte(csvInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Should normalize to 4 columns (widest row)
	assert.Contains(t, doc.RawText, "| A | B | C |  |") // padded to 4
}

func TestBuiltinParser_ParseEmptyCSV(t *testing.T) {
	p := NewBuiltinParser("csv")

	doc, err := p.Parse(context.Background(), []byte(""))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Empty CSV returns raw text
	assert.Empty(t, doc.RawText)
}

func TestBuiltinParser_ParseXML(t *testing.T) {
	p := NewBuiltinParser("xml")
	xmlInput := `<?xml version="1.0"?>
<root>
	<title>Document</title>
	<content>
		<p>First paragraph.</p>
		<p>Second paragraph with &lt;escaped&gt; chars.</p>
	</content>
</root>`

	doc, err := p.Parse(context.Background(), []byte(xmlInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// XML tags should be stripped
	assert.NotContains(t, doc.RawText, "<p>")
	assert.NotContains(t, doc.RawText, "</p>")
	// Text content should be present
	assert.Contains(t, doc.RawText, "First paragraph")
	assert.Contains(t, doc.RawText, "Second paragraph")
	// Entities should be decoded: &lt; → <
	assert.Contains(t, doc.RawText, "<escaped>")
}

func TestBuiltinParser_ParseXMLScriptStrip(t *testing.T) {
	p := NewBuiltinParser("xml")
	xmlInput := `<doc><title>Visible</title><script>hidden code</script><p>More text</p></doc>`

	doc, err := p.Parse(context.Background(), []byte(xmlInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// Script content should be stripped
	assert.NotContains(t, doc.RawText, "hidden code")
	assert.Contains(t, doc.RawText, "Visible")
	assert.Contains(t, doc.RawText, "More text")
}

func TestBuiltinParser_ParseYAML(t *testing.T) {
	p := NewBuiltinParser("yaml")
	yamlInput := "name: John\nage: 30\ncity: NYC"

	doc, err := p.Parse(context.Background(), []byte(yamlInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	// YAML is passed through as-is
	assert.Equal(t, yamlInput, doc.RawText)
}

func TestBuiltinParser_ParseYML(t *testing.T) {
	p := NewBuiltinParser("yml")
	yamlInput := "key: value\nlist:\n  - item1\n  - item2"

	doc, err := p.Parse(context.Background(), []byte(yamlInput))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, yamlInput, doc.RawText)
}

func TestBuiltinParser_ParseTXT(t *testing.T) {
	p := NewBuiltinParser("txt")
	text := "Plain text, no conversion needed."

	doc, err := p.Parse(context.Background(), []byte(text))
	assert.NoError(t, err)
	assert.Equal(t, text, doc.RawText)
}

func TestConvertToText_DefaultPassthrough(t *testing.T) {
	raw := []byte("some content")
	result := convertToText(raw, "md")
	assert.Equal(t, "some content", result)
	result = convertToText(raw, "txt")
	assert.Equal(t, "some content", result)
	result = convertToText(raw, "unknown")
	assert.Equal(t, "some content", result)
}

func TestHTMLToText_Entities(t *testing.T) {
	// HTML entities should be decoded by the tokenizer's text output
	html := "<p>&amp; &lt; &gt; &quot; &#39;</p>"
	// x/net/html tokenizer automatically decodes text content
	result := htmlToText([]byte(html))
	assert.Contains(t, result, "&") // &amp; → &
	assert.NotContains(t, result, "&amp;") // already decoded
}

func TestHTMLToText_BR(t *testing.T) {
	html := "<p>Line 1<br>Line 2<br/>Line 3</p>"
	result := htmlToText([]byte(html))
	assert.Contains(t, result, "1")
	assert.Contains(t, result, "2")
	assert.Contains(t, result, "3")
}

func TestCSVToMarkdown_SingleRow(t *testing.T) {
	csv := "A,B,C"
	result := csvToMarkdown([]byte(csv))
	assert.Contains(t, result, "| A | B | C |")
	assert.Contains(t, result, "| --- | --- | --- |")
	// Single row (header only) should have header + separator but no data rows
}

func TestXMLToText_Empty(t *testing.T) {
	result := xmlToText([]byte("<root></root>"))
	assert.Empty(t, result)
}

func TestJSONToMarkdown_SmallObject(t *testing.T) {
	result := jsonToMarkdown([]byte(`{"key":"val"}`))
	assert.Contains(t, result, "```json")
	assert.Contains(t, result, `"key"`)
}

// assertValidJSONBlocks verifies that each ```json block in the text contains valid JSON.
func assertValidJSONBlocks(t *testing.T, text string) {
	t.Helper()
	parts := strings.Split(text, "```json")
	for i, part := range parts {
		if i == 0 || part == "" {
			continue
		}
		// Extract content before closing ```
		idx := strings.Index(part, "```")
		if idx < 0 {
			continue
		}
		jsonStr := strings.TrimSpace(part[:idx])
		assert.True(t, json.Valid([]byte(jsonStr)), "block %d: invalid JSON: %s", i, jsonStr[:minLen(jsonStr, 80)])
	}
}

func minLen(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}

// recordParser is a test helper that records call order.
type recordParser struct {
	name   string
	result *domain.ParsedDocument
	err    error
	record *[]string
}

func (p *recordParser) Name() string { return p.name }

func (p *recordParser) Parse(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	*p.record = append(*p.record, p.name)
	return p.result, p.err
}
