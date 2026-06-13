package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

func headingDoc(rawText string, sections []domain.Section) *domain.ParsedDocument {
	return &domain.ParsedDocument{RawText: rawText, Sections: sections}
}

func sect(title string, level int, startByte, endByte int) domain.Section {
	return domain.Section{Title: title, Level: level, StartPos: startByte, EndPos: endByte}
}

func headingCfg(chunkSize, overlap int) domain.ChunkingConfig {
	return domain.ChunkingConfig{
		ChunkSize:  chunkSize,
		Overlap:    overlap,
		Separators: []string{"\n\n", "\n", "。", ". ", " "},
	}
}

// ---------------------------------------------------------------------------
// byteToRuneOffsets
// ---------------------------------------------------------------------------

func TestByteToRuneOffsets_ASCII(t *testing.T) {
	text := "hello world"
	offsets := byteToRuneOffsets(text)
	if len(offsets) != len(text)+1 {
		t.Fatalf("expected %d, got %d", len(text)+1, len(offsets))
	}
	for i := 0; i <= len(text); i++ {
		if offsets[i] != i {
			t.Fatalf("byte %d: expected rune %d, got %d", i, i, offsets[i])
		}
	}
}

func TestByteToRuneOffsets_CJK(t *testing.T) {
	text := "你好世界"
	offsets := byteToRuneOffsets(text)
	if offsets[0] != 0 {
		t.Fatalf("byte 0: expected rune 0, got %d", offsets[0])
	}
	if offsets[3] != 1 {
		t.Fatalf("byte 3: expected rune 1, got %d", offsets[3])
	}
	if offsets[6] != 2 {
		t.Fatalf("byte 6: expected rune 2, got %d", offsets[6])
	}
	if offsets[9] != 3 {
		t.Fatalf("byte 9: expected rune 3, got %d", offsets[9])
	}
	if offsets[12] != 4 {
		t.Fatalf("byte 12: expected rune 4, got %d", offsets[12])
	}
}

func TestByteToRuneOffsets_Mixed(t *testing.T) {
	text := "A你好B"
	offsets := byteToRuneOffsets(text)
	if len(offsets) != len(text)+1 {
		t.Fatalf("expected length %d, got %d", len(text)+1, len(offsets))
	}
	if offsets[1] != 1 {
		t.Fatalf("byte 1: expected rune 1, got %d", offsets[1])
	}
	if offsets[4] != 2 {
		t.Fatalf("byte 4: expected rune 2, got %d", offsets[4])
	}
	if offsets[7] != 3 {
		t.Fatalf("byte 7: expected rune 3, got %d", offsets[7])
	}
	if offsets[8] != 4 {
		t.Fatalf("byte 8: expected rune 4, got %d", offsets[8])
	}
}

func TestByteToRuneOffsets_Empty(t *testing.T) {
	offsets := byteToRuneOffsets("")
	if len(offsets) != 1 || offsets[0] != 0 {
		t.Fatalf("expected [0], got %v", offsets)
	}
}

func TestByteToRuneOffsets_Newline(t *testing.T) {
	text := "a\nb\nc"
	offsets := byteToRuneOffsets(text)
	for i := 0; i <= len(text); i++ {
		if offsets[i] != i {
			t.Fatalf("byte %d: expected rune %d, got %d", i, i, offsets[i])
		}
	}
}

// ---------------------------------------------------------------------------
// hasRealHeadings
// ---------------------------------------------------------------------------

func TestHasRealHeadings_True(t *testing.T) {
	if !hasRealHeadings([]domain.Section{{Level: 0}, {Level: 1, Title: "Intro"}}) {
		t.Fatal("expected true")
	}
}

func TestHasRealHeadings_False(t *testing.T) {
	if hasRealHeadings([]domain.Section{{Level: 0}}) {
		t.Fatal("expected false")
	}
	if hasRealHeadings(nil) {
		t.Fatal("expected false for nil")
	}
	if hasRealHeadings([]domain.Section{}) {
		t.Fatal("expected false for empty")
	}
}

// ---------------------------------------------------------------------------
// headingAwareChunk — basic
// ---------------------------------------------------------------------------

func TestHeadingAware_BasicMarkdown(t *testing.T) {
	// len=111: "# Title\n"=8 + "This is the intro paragraph.\n"=30 + "\n"=1 + "## Section A\n"=13
	//          + "Content for section A.\n"=23 + "\n"=1 + "## Section B\n"=13 + "Content for section B.\n"=23
	text := "# Title\nThis is the intro paragraph.\n\n## Section A\nContent for section A.\n\n## Section B\nContent for section B.\n"
	sections := []domain.Section{
		sect("Title", 1, 0, 38),
		sect("Section A", 2, 38, 75),
		sect("Section B", 2, 75, 112), // clamped to 111 by code
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	checkPath := func(chunk domain.Chunk, expectedTitles ...string) {
		hp, ok := chunk.Metadata["heading_path"]
		if !ok {
			t.Fatalf("chunk %d: missing heading_path", chunk.Index)
		}
		path := hp.([]map[string]any)
		if len(path) != len(expectedTitles) {
			t.Fatalf("chunk %d: expected %d path entries, got %d", chunk.Index, len(expectedTitles), len(path))
		}
		for i, title := range expectedTitles {
			if path[i]["title"] != title {
				t.Fatalf("chunk %d path[%d]: expected %q, got %q", chunk.Index, i, title, path[i]["title"])
			}
		}
	}
	checkPath(chunks[0], "Title")
	checkPath(chunks[1], "Title", "Section A")
	checkPath(chunks[2], "Title", "Section B")
}

func TestHeadingAware_Preamble(t *testing.T) {
	text := "Some preamble text.\nMore preamble.\n\n# H1\nBody under H1.\n"
	sections := []domain.Section{
		sect("", 0, 0, 36),
		sect("H1", 1, 36, 57),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Preamble: empty heading_path
	hp0 := chunks[0].Metadata["heading_path"].([]map[string]any)
	if len(hp0) != 0 {
		t.Fatalf("preamble should have empty heading_path, got %v", hp0)
	}
	// H1: has heading
	hp1 := chunks[1].Metadata["heading_path"].([]map[string]any)
	if len(hp1) == 0 || hp1[len(hp1)-1]["title"] != "H1" {
		t.Fatalf("H1 chunk heading_path: %v", hp1)
	}
}

func TestHeadingAware_EmptyText(t *testing.T) {
	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc("", nil), headingCfg(512, 50))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
}

// ---------------------------------------------------------------------------
// Oversized section
// ---------------------------------------------------------------------------

func TestHeadingAware_OversizedSection(t *testing.T) {
	largeBody := strings.Repeat("word word word word word.\n", 30)
	text := "# Big Section\n" + largeBody + "\n## Small Section\nTiny.\n"

	h1End := len("# Big Section\n") + len(largeBody) + len("\n")
	sections := []domain.Section{
		sect("Big Section", 1, 0, h1End),
		sect("Small Section", 2, h1End, len(text)+1),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(50, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected >= 3 chunks, got %d", len(chunks))
	}

	var h1Count, h2Count int
	for _, c := range chunks {
		hp := c.Metadata["heading_path"].([]map[string]any)
		last := hp[len(hp)-1]["title"].(string)
		switch last {
		case "Big Section":
			h1Count++
		case "Small Section":
			h2Count++
		}
	}
	if h1Count < 2 {
		t.Fatalf("expected >= 2 H1 sub-chunks, got %d", h1Count)
	}
	if h2Count != 1 {
		t.Fatalf("expected 1 H2 chunk, got %d", h2Count)
	}
}

// ---------------------------------------------------------------------------
// Coalesce
// ---------------------------------------------------------------------------

func TestHeadingAware_TinyCoalesce(t *testing.T) {
	text := "# H1\nIntro line.\n\n## H2a\nOne.\n\n## H2b\nTwo.\n\n## H2c\nThree.\n"
	sections := []domain.Section{
		sect("H1", 1, 0, 18),
		sect("H2a", 2, 18, 31),
		sect("H2b", 2, 31, 44),
		sect("H2c", 2, 44, 59),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks after coalescing, got %d", len(chunks))
	}
	for i, c := range chunks {
		if _, ok := c.Metadata["heading_path"]; !ok {
			t.Fatalf("chunk %d missing heading_path", i)
		}
	}
}

func TestHeadingAware_CoalesceRespectsBreadcrumb(t *testing.T) {
	text := "# H1a\nTiny.\n\n## H2a\nTiny.\n\n# H1b\nTiny.\n\n## H2b\nTiny.\n"
	sections := []domain.Section{
		sect("H1a", 1, 0, 13),
		sect("H2a", 2, 13, 27),
		sect("H1b", 1, 27, 40),
		sect("H2b", 2, 40, 54),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(200, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Cross-H1 merging must not happen
	for i, c := range chunks {
		hp := c.Metadata["heading_path"].([]map[string]any)
		if len(hp) == 0 {
			continue
		}
		root := hp[0]["title"].(string)
		if root != "H1a" && root != "H1b" {
			t.Fatalf("chunk %d: unexpected root title %q", i, root)
		}
	}
}

// ---------------------------------------------------------------------------
// Breadcrumb hierarchy
// ---------------------------------------------------------------------------

func TestHeadingAware_BreadcrumbPopOnSameLevel(t *testing.T) {
	text := "# H1\nA\n\n## H2a\nB\n\n### H3\nC\n\n## H2b\nD\n"
	sections := []domain.Section{
		sect("H1", 1, 0, 8),
		sect("H2a", 2, 8, 18),
		sect("H3", 3, 18, 28),
		sect("H2b", 2, 28, 38),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}

	hp2 := chunks[2].Metadata["heading_path"].([]map[string]any)
	if len(hp2) != 3 || hp2[2]["title"] != "H3" {
		t.Fatalf("H3 breadcrumb: %v", titlesFromPath(hp2))
	}

	hp3 := chunks[3].Metadata["heading_path"].([]map[string]any)
	if len(hp3) != 2 || hp3[1]["title"] != "H2b" {
		t.Fatalf("H2b breadcrumb: %v", titlesFromPath(hp3))
	}
}

func TestHeadingAware_BreadcrumbPopOnHigherLevel(t *testing.T) {
	text := "### H3\nDeep content.\n\n# H1\nTop level.\n"
	sections := []domain.Section{
		sect("H3", 3, 0, 22),
		sect("H1", 1, 22, 39),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	hp0 := chunks[0].Metadata["heading_path"].([]map[string]any)
	if len(hp0) != 1 || hp0[0]["title"] != "H3" {
		t.Fatalf("H3: %v", titlesFromPath(hp0))
	}
	hp1 := chunks[1].Metadata["heading_path"].([]map[string]any)
	if len(hp1) != 1 || hp1[0]["title"] != "H1" {
		t.Fatalf("H1: %v", titlesFromPath(hp1))
	}
}

// ---------------------------------------------------------------------------
// Position consistency
// ---------------------------------------------------------------------------

func TestHeadingAware_PositionsAreRuneOffsets(t *testing.T) {
	text := "# 标题\n中文内容。\n\n## 第二节\n更多内容。\n"
	sections := []domain.Section{
		sect("标题", 1, 0, 26),
		sect("第二节", 2, 26, 56),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, c := range chunks {
		runeLen := utf8.RuneCountInString(c.Content)
		if c.EndPos-c.StartPos != runeLen {
			t.Fatalf("chunk %d: rune count %d != position delta %d", i, runeLen, c.EndPos-c.StartPos)
		}
	}
}

func TestHeadingAware_NoPositionGaps(t *testing.T) {
	text := "# A\nContent A.\n\n## B\nContent B.\n\n## C\nContent C.\n"
	sections := []domain.Section{
		sect("A", 1, 0, 16),
		sect("B", 2, 16, 33),
		sect("C", 2, 33, 50),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].StartPos != chunks[i-1].EndPos {
			t.Fatalf("gap between chunk %d (EndPos=%d) and %d (StartPos=%d)",
				i-1, chunks[i-1].EndPos, i, chunks[i].StartPos)
		}
	}
	totalRunes := utf8.RuneCountInString(text)
	if chunks[len(chunks)-1].EndPos != totalRunes {
		t.Fatalf("last EndPos=%d, expected %d", chunks[len(chunks)-1].EndPos, totalRunes)
	}
}

// ---------------------------------------------------------------------------
// Restoration
// ---------------------------------------------------------------------------

func TestHeadingAware_RestorePlainText(t *testing.T) {
	text := "# Intro\nWelcome to the document.\n\n## Getting Started\nFirst steps.\n\n## Advanced\nDeep dive.\n"
	sections := []domain.Section{
		sect("Intro", 1, 0, 34),
		sect("Getting Started", 2, 34, 67),
		sect("Advanced", 2, 67, 91),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	runes := []rune(text)
	var restored strings.Builder
	for _, c := range chunks {
		restored.WriteString(string(runes[c.StartPos:c.EndPos]))
	}
	if restored.String() != text {
		t.Fatalf("restored text mismatch:\nexpected: %q\ngot:      %q", text, restored.String())
	}
}

// ---------------------------------------------------------------------------
// Protected content
// ---------------------------------------------------------------------------

func TestHeadingAware_ProtectedCodeBlock(t *testing.T) {
	text := "# Section\nText before.\n\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n\nMore text.\n"
	sections := []domain.Section{
		sect("Section", 1, 0, len(text)),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(30, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundIntact := false
	for _, c := range chunks {
		if strings.Contains(c.Content, "func main()") && strings.Contains(c.Content, "println") {
			foundIntact = true
			break
		}
	}
	if !foundIntact {
		t.Fatal("code block was split across chunks")
	}
}

// ---------------------------------------------------------------------------
// Parent-child
// ---------------------------------------------------------------------------

func TestHeadingAware_ParentChild_Metadata(t *testing.T) {
	text := "# H1\nContent under H1.\n\n## H2\nContent under H2.\n"
	sections := []domain.Section{
		sect("H1", 1, 0, 24),
		sect("H2", 2, 24, 49),
	}

	chunker := NewChunker()
	cfg := headingCfg(200, 0)
	cfg.ParentChild = domain.ParentChildConfig{
		Enabled:    true,
		ParentSize: 200,
		ChildSize:  50,
	}

	result, err := chunker.headingAwareParentChild(headingDoc(text, sections), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Parents) == 0 {
		t.Fatal("expected parent chunks")
	}
	if len(result.Children) == 0 {
		t.Fatal("expected child chunks")
	}

	for i, p := range result.Parents {
		if bt, _ := p.Metadata["block_type"]; bt != "parent" {
			t.Fatalf("parent %d: block_type=%v", i, bt)
		}
		if _, ok := p.Metadata["heading_path"]; !ok {
			t.Fatalf("parent %d: missing heading_path", i)
		}
	}
	for i, c := range result.Children {
		if bt, _ := c.Metadata["block_type"]; bt != "child" {
			t.Fatalf("child %d: block_type=%v", i, bt)
		}
		if _, ok := c.Metadata["parent_index"]; !ok {
			t.Fatalf("child %d: missing parent_index", i)
		}
	}
}

// ---------------------------------------------------------------------------
// No headings
// ---------------------------------------------------------------------------

func TestHeadingAware_NoHeadings(t *testing.T) {
	text := "Plain text without any markdown headings.\nJust paragraphs.\n\nMore paragraphs.\n"
	sections := []domain.Section{
		sect("", 0, 0, len(text)+1),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(50, 10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected some chunks")
	}
	for i, c := range chunks {
		if hp, ok := c.Metadata["heading_path"]; ok {
			path := hp.([]map[string]any)
			if len(path) != 0 {
				t.Fatalf("chunk %d: non-heading doc should have no path, got %v", i, path)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// CJK
// ---------------------------------------------------------------------------

func TestHeadingAware_CJKHeadings(t *testing.T) {
	text := "# 简介\n这是简介内容。\n\n## 安装\n安装步骤说明。\n\n## 配置\n配置说明。\n"
	sections := []domain.Section{
		sect("简介", 1, 0, 32),
		sect("安装", 2, 32, 65),
		sect("配置", 2, 65, 92),
	}

	chunker := NewChunker()
	chunks, err := chunker.headingAwareChunk(headingDoc(text, sections), headingCfg(500, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	want := []string{"简介", "安装", "配置"}
	for i, c := range chunks {
		hp := c.Metadata["heading_path"].([]map[string]any)
		if hp[len(hp)-1]["title"] != want[i] {
			t.Fatalf("chunk %d: expected %q, got %q", i, want[i], hp[len(hp)-1]["title"])
		}
	}
}

func titlesFromPath(path []map[string]any) []string {
	titles := make([]string, len(path))
	for i, n := range path {
		titles[i] = n["title"].(string)
	}
	return titles
}
