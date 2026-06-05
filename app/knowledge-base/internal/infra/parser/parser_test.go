package parser

import (
	"context"
	"errors"
	"testing"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestBuiltinParser_Name(t *testing.T) {
	p := NewBuiltinParser()
	assert.Equal(t, "builtin", p.Name())
}

func TestBuiltinParser_ParseMarkdown(t *testing.T) {
	p := NewBuiltinParser()
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
	p := NewBuiltinParser()
	text := "This is a plain text document without any headings.\nJust some content."

	doc, err := p.Parse(context.Background(), []byte(text))
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, text, doc.RawText)
	assert.NotEmpty(t, doc.Sections)
}

func TestBuiltinParser_ParseEmptyText(t *testing.T) {
	p := NewBuiltinParser()
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
			p := NewBuiltinParser()
			doc, err := p.Parse(context.Background(), []byte(tt.text))
			assert.NoError(t, err)
			assert.Equal(t, tt.wantLang, doc.Metadata.Language)
		})
	}
}

func TestBuiltinParser_DetectsHeadings(t *testing.T) {
	p := NewBuiltinParser()
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
	p := NewBuiltinParser()
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
