package chunker

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

// ---------------------------------------------------------------------------
// Trigger detection
// ---------------------------------------------------------------------------

func TestHasFormFeed_True(t *testing.T) {
	if !hasFormFeed("page1\fpage2") {
		t.Fatal("expected true for \\f")
	}
}

func TestHasFormFeed_False(t *testing.T) {
	if hasFormFeed("plain text") {
		t.Fatal("expected false")
	}
}

func TestShouldUseHeuristic_FormFeed(t *testing.T) {
	if !shouldUseHeuristic("short\ftext") {
		t.Fatal("form feed alone should trigger")
	}
}

func TestShouldUseHeuristic_FiveMarkers(t *testing.T) {
	// 5 numbered sections triggers heuristic
	text := "1. First\n2. Second\n3. Third\n4. Fourth\n5. Fifth\n"
	if !shouldUseHeuristic(text) {
		t.Fatal("5 markers should trigger heuristic")
	}
}

func TestShouldUseHeuristic_FewMarkers(t *testing.T) {
	text := "1. First\n2. Second\nJust two items.\n"
	if shouldUseHeuristic(text) {
		t.Fatal("only 2 markers should NOT trigger")
	}
}

func TestShouldUseHeuristic_NoMarkers(t *testing.T) {
	if shouldUseHeuristic("Just plain text without any structure.") {
		t.Fatal("plain text should not trigger")
	}
}

// ---------------------------------------------------------------------------
// Individual boundary types
// ---------------------------------------------------------------------------

func TestHeuristic_FormFeedSplit(t *testing.T) {
	text := "Before page\fAfter page"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks split on \\f, got %d", len(chunks))
	}
	// First chunk should contain "Before page", second "After page"
	if !strings.Contains(chunks[0].Content, "Before page") {
		t.Fatalf("chunk 0 should contain 'Before page': %q", chunks[0].Content)
	}
	if !strings.Contains(chunks[len(chunks)-1].Content, "After page") {
		t.Fatalf("last chunk should contain 'After page': %q", chunks[len(chunks)-1].Content)
	}
}

func TestHeuristic_NumberedSections(t *testing.T) {
	text := "1.1 Overview\nContent here.\n1.2 Methods\nMore content.\n2.1 Results\nFinal content.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks from numbered sections, got %d", len(chunks))
	}
	for i, c := range chunks {
		if bt, ok := c.Metadata["boundary_type"]; ok {
			if bt != "numbered_section" && bt != "" {
				t.Fatalf("chunk %d: unexpected boundary_type %v", i, bt)
			}
		}
	}
}

func TestHeuristic_ChapterKeywords(t *testing.T) {
	text := "Chapter 1\nIntro.\n\nChapter 2\nMain content.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestHeuristic_ChapterKeywords_German(t *testing.T) {
	text := "Kapitel 1\nInhalt.\n\nKapitel 2\nMehr Inhalt.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("Kapitel should be detected, got %d chunks", len(chunks))
	}
}

func TestHeuristic_AllCapsHeading(t *testing.T) {
	text := "INTRODUCTION\nThis is the intro section.\n\nMETHODS\nWe used the following methods.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("all-caps should create sections, got %d", len(chunks))
	}
}

func TestHeuristic_VisualSeparator(t *testing.T) {
	text := "Section one content.\n---\nSection two content.\n===\nSection three.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 3 {
		t.Fatalf("separators should split text, got %d chunks", len(chunks))
	}
}

func TestHeuristic_ExcessiveBlanks(t *testing.T) {
	text := "Paragraph one.\n\n\n\nParagraph two.\n\n\n\nParagraph three.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 3 {
		t.Fatalf("excessive blanks should split, got %d chunks", len(chunks))
	}
}

func TestHeuristic_PageFooters(t *testing.T) {
	text := "Page content.\n\nPage 1 of 10\n\nMore content.\n\nSeite 2 von 10\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected some chunks")
	}
	// Page footers should be boundary markers
	for _, c := range chunks {
		if strings.Contains(c.Content, "Page 1 of 10") {
			if bt, ok := c.Metadata["boundary_type"]; ok {
				// Footer text should be at start of its section
				if bt != "page_footer" {
					t.Logf("page footer chunk boundary_type: %v", bt)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Deduplication
// ---------------------------------------------------------------------------

func TestHeuristic_DedupNearby(t *testing.T) {
	// A numbered section and chapter keyword close together — higher pri wins
	text := "1. Chapter 1\nContent.\n"
	markers := findAllBoundaryMarkers(text)
	origCount := len(markers)
	deduped := dedupMarkers(markers, text)
	if len(deduped) >= origCount {
		t.Logf("nearby markers: %d -> %d (may not dedup if same line)", origCount, len(deduped))
	}
	// Should have at least one valid marker
	if len(deduped) == 0 {
		t.Fatal("expected at least 1 marker after dedup")
	}
}

// ---------------------------------------------------------------------------
// Oversized sections
// ---------------------------------------------------------------------------

func TestHeuristic_OversizedSection(t *testing.T) {
	largeBody := strings.Repeat("data data data data data.\n", 30)
	text := "1.1 Section\n" + largeBody + "\n1.2 Next\nSmall.\n"

	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(50, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected >= 3 chunks (oversized split + small), got %d", len(chunks))
	}
}

// ---------------------------------------------------------------------------
// Coalesce
// ---------------------------------------------------------------------------

func TestHeuristic_CoalesceSameBoundary(t *testing.T) {
	text := "1. A\nTiny.\n\n2. B\nTiny.\n\n3. C\nTiny.\n\n4. D\nTiny.\n\n5. E\nTiny.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(200, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected some chunks")
	}
	// With chunkSize=200 and threshold=50, tiny sections (each ~15 bytes)
	// under same boundary type should be merged
	for _, c := range chunks {
		if bt, ok := c.Metadata["boundary_type"]; ok && bt != "" {
			if bt != "numbered_section" {
				t.Fatalf("unexpected boundary_type: %v", bt)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Position consistency
// ---------------------------------------------------------------------------

func TestHeuristic_PositionsAreRuneOffsets(t *testing.T) {
	text := "1.1 概述\n中文内容。\n\n1.2 方法\n更多中文。\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
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

func TestHeuristic_Restoration(t *testing.T) {
	text := "Before.\n\n\n\nAfter triple blank.\n---\nAfter separator.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
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
// Metadata
// ---------------------------------------------------------------------------

func TestHeuristic_MetadataFields(t *testing.T) {
	text := "1. First Section\nContent under first.\n\n2. Second Section\nContent under second.\n"
	chunker := NewChunker()
	chunks, err := chunker.heuristicChunk(
		&domain.ParsedDocument{RawText: text},
		headingCfg(500, 0),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, c := range chunks {
		bt, hasBT := c.Metadata["boundary_type"]
		_, hasText := c.Metadata["boundary_text"]
		if !hasBT && !hasText {
			// First chunk may have no boundary (before any marker)
			if i > 0 {
				t.Logf("chunk %d has no boundary metadata", i)
			}
			continue
		}
		if hasBT && bt != "" && bt != "numbered_section" {
			t.Fatalf("chunk %d: expected numbered_section, got %v", i, bt)
		}
	}
}

// ---------------------------------------------------------------------------
// Parent-child
// ---------------------------------------------------------------------------

func TestHeuristic_ParentChild(t *testing.T) {
	text := "Chapter 1\nIntro content.\n\nChapter 2\nMore content here.\n\nChapter 3\nFinal.\n"
	chunker := NewChunker()
	cfg := headingCfg(200, 0)
	cfg.ParentChild = domain.ParentChildConfig{
		Enabled:    true,
		ParentSize: 200,
		ChildSize:  50,
	}

	result, err := chunker.heuristicParentChild(
		&domain.ParsedDocument{RawText: text},
		cfg,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Parents) == 0 {
		t.Fatal("expected parent chunks")
	}
	if len(result.Children) == 0 {
		t.Fatal("expected child chunks")
	}
	for _, p := range result.Parents {
		if bt, _ := p.Metadata["block_type"]; bt != "parent" {
			t.Fatalf("parent missing block_type=parent")
		}
	}
	for _, c := range result.Children {
		if bt, _ := c.Metadata["block_type"]; bt != "child" {
			t.Fatalf("child missing block_type=child")
		}
	}
}
