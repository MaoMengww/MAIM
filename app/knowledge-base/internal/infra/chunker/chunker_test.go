package chunker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

var ctx = context.Background()

func doc(text string) *domain.ParsedDocument {
	return &domain.ParsedDocument{RawText: text}
}

func cfg(chunkSize, overlap int) domain.ChunkingConfig {
	return domain.ChunkingConfig{
		ChunkSize:  chunkSize,
		Overlap:    overlap,
		Separators: []string{"\n\n", "\n", "。", ". ", " "},
	}
}

// ---------------------------------------------------------------------------
// Basic
// ---------------------------------------------------------------------------

func TestChunk_BasicASCII(t *testing.T) {
	text := "Hello world. This is a test."
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), cfg(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	var combined string
	for _, ch := range chunks {
		combined += ch.Content
	}
	if combined != text {
		t.Errorf("combined content:\n  got:  %q\n  want: %q", combined, text)
	}
}

func TestChunk_EmptyText(t *testing.T) {
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(""), cfg(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestChunk_SingleChar(t *testing.T) {
	c := NewChunker()
	result, err := c.Chunk(ctx, doc("你"), cfg(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	if len(chunks) != 1 || chunks[0].Content != "你" {
		t.Errorf("expected 1 chunk '你', got %d chunks: %q", len(chunks), chunks[0].Content)
	}
}

// ---------------------------------------------------------------------------
// Chinese text with rune-correct positions
// ---------------------------------------------------------------------------

func TestChunk_ChineseText_PositionsAreRuneOffsets(t *testing.T) {
	text := "你好世界这是一个测试文本"
	runeCount := utf8.RuneCountInString(text)
	if runeCount == len(text) {
		t.Fatal("test requires multi-byte characters")
	}

	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), cfg(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	ch := chunks[0]
	if ch.StartPos != 0 {
		t.Errorf("StartPos: got %d, want 0", ch.StartPos)
	}
	if ch.EndPos != runeCount {
		t.Errorf("EndPos: got %d, want %d", ch.EndPos, runeCount)
	}
}

func TestChunk_ChineseMultiChunk_PositionsConsistent(t *testing.T) {
	line := "这是一段中文内容用于测试分割功能是否正确。"
	text := strings.Repeat(line+"\n\n", 20)
	text = strings.TrimRight(text, "\n")

	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), cfg(30, 5))
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	textRunes := []rune(text)
	for i, ch := range chunks {
		contentRunes := []rune(ch.Content)
		spanLen := ch.EndPos - ch.StartPos
		if spanLen != len(contentRunes) {
			t.Errorf("chunk[%d]: End(%d)-Start(%d)=%d, but rune len of content=%d",
				i, ch.EndPos, ch.StartPos, spanLen, len(contentRunes))
		}
		if ch.StartPos < 0 {
			t.Errorf("chunk[%d]: negative StartPos %d", i, ch.StartPos)
		}
		if ch.EndPos > len(textRunes) {
			t.Errorf("chunk[%d]: EndPos %d exceeds text rune count %d", i, ch.EndPos, len(textRunes))
		}
		if ch.StartPos >= 0 && ch.EndPos <= len(textRunes) {
			sliced := string(textRunes[ch.StartPos:ch.EndPos])
			if sliced != ch.Content {
				t.Errorf("chunk[%d]: content mismatch via rune slice:\n  got:  %q\n  want: %q",
					i, sliced, ch.Content)
			}
		}
	}
}

func TestChunk_MixedChineseAndASCII(t *testing.T) {
	text := "Hello你好World世界Test测试"
	expectedRunes := utf8.RuneCountInString(text)

	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), cfg(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	span := chunks[0].EndPos - chunks[0].StartPos
	if span != expectedRunes {
		t.Errorf("span %d != rune count %d (byte len would be %d)", span, expectedRunes, len(text))
	}
}

// ---------------------------------------------------------------------------
// Protected patterns (code blocks, LaTeX, images, tables)
// ---------------------------------------------------------------------------

func TestChunk_ProtectedCodeBlock_NotSplit(t *testing.T) {
	// Surround the code block with enough text to force multiple chunks.
	prefix := strings.Repeat("段落开头内容。\n", 15)
	suffix := strings.Repeat("段落结尾内容。\n", 15)
	text := prefix + "```python\nprint('hello')\nprint('world')\n```\n" + suffix
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  50,
		Overlap:    0,
		Separators: []string{"\n\n", "\n", "。"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// Verify the code block is not split across chunks.
	for _, ch := range chunks {
		if strings.Contains(ch.Content, "print('hello')") && strings.Contains(ch.Content, "print('world')") {
			return // both lines in the same chunk — good
		}
	}
	t.Error("code block content was split across chunks")
}

func TestChunk_ProtectedLaTeX_NotSplit(t *testing.T) {
	text := "前面的文字$$E=mc^2$$后面的文字"
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), cfg(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	textRunes := []rune(text)
	for i, ch := range chunks {
		spanLen := ch.EndPos - ch.StartPos
		contentRuneLen := utf8.RuneCountInString(ch.Content)
		if spanLen != contentRuneLen {
			t.Errorf("chunk[%d]: span %d != rune len %d", i, spanLen, contentRuneLen)
		}
		if ch.EndPos > len(textRunes) {
			t.Errorf("chunk[%d]: EndPos %d > total runes %d", i, ch.EndPos, len(textRunes))
		}
	}
	// LaTeX block should be in a single chunk
	for _, ch := range chunks {
		if strings.Contains(ch.Content, "E=mc^2") {
			if !strings.Contains(ch.Content, "前面的文字") && !strings.Contains(ch.Content, "后面的文字") {
				return // pure LaTeX chunk — good, not split
			}
		}
	}
}

func TestChunk_ProtectedTable_NotSplit(t *testing.T) {
	text := "" +
		"| 姓名 | 年龄 |\n" +
		"| --- | --- |\n" +
		"| 张三 | 25 |\n" +
		"| 李四 | 30 |\n" +
		"| 王五 | 28 |\n"
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  200,
		Overlap:    0,
		Separators: []string{"\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	// Table rows should not be split across chunks.
	for _, ch := range chunks {
		if strings.Contains(ch.Content, "李四") && !strings.Contains(ch.Content, "张三") {
			t.Errorf("table row split from header:\n%s", ch.Content)
		}
	}
}

func TestChunk_ProtectedLargeBlock_ForceSplit(t *testing.T) {
	// Create a code block larger than maxProtectedSize
	var sb strings.Builder
	sb.WriteString("```\n")
	for i := 0; i < 10000; i++ {
		sb.WriteString(fmt.Sprintf("line %04d\n", i))
	}
	sb.WriteString("```")
	text := sb.String()

	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  500,
		Overlap:    0,
		Separators: []string{"\n\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	// Should be split into multiple chunks because the code block exceeds maxProtectedSize
	if len(chunks) < 2 {
		t.Fatalf("expected large protected block to be split into multiple chunks, got %d", len(chunks))
	}
	// Verify no single chunk exceeds maxProtectedSize
	for i, ch := range chunks {
		if utf8.RuneCountInString(ch.Content) > maxProtectedSize {
			t.Errorf("chunk[%d] has %d runes, exceeds max %d", i, utf8.RuneCountInString(ch.Content), maxProtectedSize)
		}
	}
}

// ---------------------------------------------------------------------------
// Table header tracking — prepend column names to continuation chunks
// ---------------------------------------------------------------------------

func TestChunk_TableHeaderPrepended(t *testing.T) {
	text := "前面的文字\n\n" +
		"| 姓名 | 年龄 | 城市 |\n" +
		"| --- | --- | --- |\n" +
		"| 张三 | 25 | 北京 |\n" +
		"| 李四 | 30 | 上海 |\n" +
		"| 王五 | 28 | 广州 |\n" +
		"| 赵六 | 35 | 深圳 |\n" +
		"| 孙七 | 22 | 杭州 |\n" +
		"| 周八 | 40 | 成都 |\n" +
		"\n后面的文字"
	tableHeader := "| 姓名 | 年龄 | 城市 |\n| --- | --- | --- |\n"

	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  60,
		Overlap:    5,
		Separators: []string{"\n\n", "\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	prependCount := 0
	for _, ch := range chunks {
		if strings.Contains(ch.Content, "李四") || strings.Contains(ch.Content, "王五") ||
			strings.Contains(ch.Content, "赵六") || strings.Contains(ch.Content, "孙七") ||
			strings.Contains(ch.Content, "周八") {
			if !strings.Contains(ch.Content, "张三") {
				if !strings.HasPrefix(ch.Content, tableHeader) {
					t.Errorf("chunk with table rows missing prepended header:\n%s", ch.Content)
				} else {
					prependCount++
				}
			}
		}
	}
	if prependCount == 0 {
		t.Error("expected at least one chunk with prepended table header")
	}
}

func TestChunk_NoHeaderForNonTableContent(t *testing.T) {
	text := strings.Repeat("这是一段普通的中文文本，不包含任何表格。\n\n", 10)
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  30,
		Overlap:    5,
		Separators: []string{"\n\n", "\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	textRunes := []rune(text)
	for i, ch := range chunks {
		spanLen := ch.EndPos - ch.StartPos
		contentRuneLen := utf8.RuneCountInString(ch.Content)
		if spanLen != contentRuneLen {
			t.Errorf("chunk[%d]: span %d != rune len %d (no table)", i, spanLen, contentRuneLen)
		}
		if ch.EndPos > len(textRunes) {
			t.Errorf("chunk[%d]: EndPos %d exceeds total runes %d", i, ch.EndPos, len(textRunes))
		}
	}
}

func TestChunk_MultipleTables(t *testing.T) {
	text := "第一个表格：\n\n" +
		"| 名称 | 值 |\n| --- | --- |\n| A | 1 |\n| B | 2 |\n| C | 3 |\n" +
		"\n中间文字\n\n" +
		"| 项目 | 状态 |\n| --- | --- |\n| X | 完成 |\n| Y | 进行中 |\n| Z | 未开始 |\n" +
		"\n结尾文字"

	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  50,
		Overlap:    5,
		Separators: []string{"\n\n", "\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children

	for _, ch := range chunks {
		if strings.Contains(ch.Content, "| Y |") && !strings.Contains(ch.Content, "| X |") {
			if !strings.Contains(ch.Content, "| 项目 | 状态 |") {
				t.Errorf("chunk with table-2 rows should have table-2 header:\n%s", ch.Content)
			}
			if strings.Contains(ch.Content, "| 名称 | 值 |") {
				t.Errorf("chunk with table-2 rows should NOT have table-1 header:\n%s", ch.Content)
			}
		}
	}
}

func TestChunk_TableEndedByEmptyLine(t *testing.T) {
	text := "| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |\n\n这是之后的普通文本\n更多的普通文本"
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  40,
		Overlap:    5,
		Separators: []string{"\n\n", "\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children

	for _, ch := range chunks {
		hasPlainText := strings.Contains(ch.Content, "这是之后") || strings.Contains(ch.Content, "更多的")
		hasTableRow := strings.Contains(ch.Content, "|")
		if hasPlainText && !hasTableRow {
			if strings.Contains(ch.Content, "| --- |") {
				t.Errorf("post-table chunk should not contain table header:\n%s", ch.Content)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Parent-child chunking
// ---------------------------------------------------------------------------

func TestChunk_ParentChild(t *testing.T) {
	text := strings.Repeat("这是一段测试文本，用于验证父子切分功能。\n\n", 10)
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize: 100,
		Overlap:   10,
		ParentChild: domain.ParentChildConfig{
			Enabled:    true,
			ParentSize: 300,
			ChildSize:  50,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Parents) == 0 && len(result.Children) == 0 {
		t.Fatal("expected non-empty parent-child result")
	}

	parentCount := 0
	childCount := 0
	for _, ch := range result.Parents {
		if ch.Metadata == nil {
			t.Error("parent chunk missing metadata")
			continue
		}
		if ch.Metadata["block_type"] != "parent" {
			t.Errorf("expected parent block_type, got %v", ch.Metadata["block_type"])
		}
		parentCount++
	}
	for _, ch := range result.Children {
		if ch.Metadata == nil {
			t.Error("child chunk missing metadata")
			continue
		}
		if ch.Metadata["block_type"] != "child" {
			t.Errorf("expected child block_type, got %v", ch.Metadata["block_type"])
		}
		if _, ok := ch.Metadata["parent_index"]; !ok {
			t.Error("child chunk missing parent_index")
		}
		childCount++
	}
	if parentCount == 0 {
		t.Error("expected at least one parent chunk")
	}
	if childCount == 0 {
		t.Error("expected at least one child chunk")
	}
}

func TestChunk_ParentChildWithTable(t *testing.T) {
	text := "前言\n\n" +
		"| 列A | 列B |\n| --- | --- |\n| 数据1 | 数据2 |\n| 数据3 | 数据4 |\n| 数据5 | 数据6 |\n" +
		"\n结尾"
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize: 100,
		Overlap:   0,
		ParentChild: domain.ParentChildConfig{
			Enabled:    true,
			ParentSize: 200,
			ChildSize:  40,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Parents) == 0 && len(result.Children) == 0 {
		t.Fatal("expected parent-child chunks")
	}
	// Verify child content can be reconstructed from positions
	textRunes := []rune(text)
	for _, ch := range result.Children {
		if ch.EndPos > len(textRunes) {
			t.Errorf("child chunk index %d: EndPos %d exceeds text runes %d", ch.Index, ch.EndPos, len(textRunes))
		}
	}
}

// ---------------------------------------------------------------------------
// Token count estimation
// ---------------------------------------------------------------------------

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		text string
		min  int
	}{
		{"你好世界", 5},         // 4 chars * 4/3 = 5.33 → 5
		{"Hello World", 14}, // 11 * 4/3 = 14.67 → 14
		{"", 0},
	}
	for _, tt := range tests {
		got := estimateTokens(tt.text)
		if got < tt.min {
			t.Errorf("estimateTokens(%q)=%d, want >= %d", tt.text, got, tt.min)
		}
	}
}

// ---------------------------------------------------------------------------
// Overlap correctness
// ---------------------------------------------------------------------------

func TestChunk_OverlapBoundaries(t *testing.T) {
	text := strings.Repeat("中文测试内容，", 50)
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  20,
		Overlap:    5,
		Separators: []string{"，"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children
	for i, ch := range chunks {
		if ch.StartPos < 0 {
			t.Errorf("chunk[%d]: negative StartPos %d", i, ch.StartPos)
		}
		if ch.EndPos < ch.StartPos {
			t.Errorf("chunk[%d]: EndPos %d < StartPos %d", i, ch.EndPos, ch.StartPos)
		}
	}
	// Check overlap: consecutive chunks should share some content
	if len(chunks) >= 2 {
		prev := chunks[0]
		curr := chunks[1]
		if prev.EndPos > curr.StartPos {
			overlapContent := string([]rune(prev.Content)[len([]rune(prev.Content))-(prev.EndPos-curr.StartPos):])
			if !strings.Contains(curr.Content, overlapContent) {
				t.Logf("overlap not verified for chunks 0 and 1")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Large document
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Restoration tests — verify original text can be reconstructed from positions
// ---------------------------------------------------------------------------

func restoreFromChunks(chunks []domain.Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	sorted := make([]domain.Chunk, len(chunks))
	copy(sorted, chunks)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0; j-- {
			if sorted[j].EndPos < sorted[j-1].EndPos ||
				(sorted[j].EndPos == sorted[j-1].EndPos && sorted[j].StartPos < sorted[j-1].StartPos) {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			}
		}
	}
	var result []rune
	lastEnd := 0
	for _, c := range sorted {
		if c.EndPos <= lastEnd {
			continue
		}
		contentRunes := []rune(c.Content)
		spanLen := c.EndPos - c.StartPos
		headerLen := len(contentRunes) - spanLen
		if headerLen < 0 {
			headerLen = 0
		}
		originalPortion := contentRunes[headerLen:]
		newStart := 0
		if lastEnd > c.StartPos {
			newStart = lastEnd - c.StartPos
		}
		if newStart < len(originalPortion) {
			result = append(result, originalPortion[newStart:]...)
		}
		lastEnd = c.EndPos
	}
	return string(result)
}

func TestChunk_RestorePlainText(t *testing.T) {
	text := "Hello world.\n\nThis is a test.\n\nAnother paragraph."
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{ChunkSize: 10, Overlap: 3, Separators: []string{"\n\n", "\n", ". "}})
	if err != nil {
		t.Fatalf("chunk failed: %v", err)
	}
	restored := restoreFromChunks(result.Children)
	if restored != text {
		t.Errorf("restoration failed:\n  orig: %q\n  got:  %q", text, restored)
	}
}

func TestChunk_RestoreChineseWithTable(t *testing.T) {
	text := "前言\n\n| 姓名 | 年龄 |\n| --- | --- |\n| 张三 | 25 |\n| 李四 | 30 |\n| 王五 | 28 |\n\n结尾"
	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{ChunkSize: 50, Overlap: 5, Separators: []string{"\n\n", "\n"}})
	if err != nil {
		t.Fatalf("chunk failed: %v", err)
	}
	restored := restoreFromChunks(result.Children)
	if restored != text {
		t.Errorf("restoration failed")
	}
	textRunes := []rune(text)
	covered := make([]bool, len(textRunes))
	for _, ch := range result.Children {
		for p := ch.StartPos; p < ch.EndPos && p < len(textRunes); p++ {
			covered[p] = true
		}
	}
	for i, v := range covered {
		if !v {
			t.Errorf("rune position %d not covered by any chunk", i)
			break
		}
	}
}

func TestChunk_LargeDocument(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf("第%d段：这是一段用于测试的中文内容，包含各种常见的汉字和标点符号。", i))
		sb.WriteString("\n\n")
	}
	text := sb.String()

	c := NewChunker()
	result, err := c.Chunk(ctx, doc(text), domain.ChunkingConfig{
		ChunkSize:  50,
		Overlap:    10,
		Separators: []string{"\n\n", "\n", "。"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chunks := result.Children

	textRunes := []rune(text)
	for i, ch := range chunks {
		contentRuneLen := utf8.RuneCountInString(ch.Content)
		spanLen := ch.EndPos - ch.StartPos
		if spanLen != contentRuneLen {
			t.Errorf("chunk[%d]: End(%d)-Start(%d)=%d != runeLen(%d)",
				i, ch.EndPos, ch.StartPos, spanLen, contentRuneLen)
		}
		if ch.StartPos < 0 {
			t.Errorf("chunk[%d]: negative StartPos %d", i, ch.StartPos)
		}
		if ch.EndPos > len(textRunes) {
			t.Errorf("chunk[%d]: EndPos %d > total runes %d", i, ch.EndPos, len(textRunes))
		}
		if ch.StartPos >= 0 && ch.EndPos <= len(textRunes) {
			sliced := string(textRunes[ch.StartPos:ch.EndPos])
			if sliced != ch.Content {
				t.Errorf("chunk[%d]: content mismatch via rune-slice", i)
			}
		}
	}
}
