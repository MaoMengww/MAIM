package chunker

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// boundaryType encodes both the priority and semantic kind of a heuristic boundary.
// Higher numeric value = higher priority (used for dedup resolution).
type boundaryType int

const (
	boundaryFormFeed        boundaryType = 100 // \f form feed (PDF page break)
	boundaryNumberedSection boundaryType = 90  // "1.2.3 Title", "§1", "第1节"
	boundaryChapterKeyword  boundaryType = 85  // "Chapter 3", "Kapitel 3", "第一章"
	boundaryAllCapsHeading  boundaryType = 70  // ALL CAPS SHORT LINE
	boundaryVisualSeparator boundaryType = 60  // ---, ===, ***
	boundaryPageFooter      boundaryType = 50  // "Page 1 of 10", "Seite 1 von 10"
	boundaryExcessiveBlanks boundaryType = 40  // \n{3,} (3+ consecutive blank lines)
)

// boundaryMarker represents a detected heuristic boundary in the text.
type boundaryMarker struct {
	priority boundaryType
	position int    // byte position where the marker starts
	length   int    // byte length of the matched text
	text     string // the matched text (for metadata)
}

// heuristicSection is an intermediate chunk produced by heuristic boundary splitting.
type heuristicSection struct {
	content      string
	start, end   int          // rune offsets in the original document
	boundaryType boundaryType // which boundary precedes this section
	boundaryText string       // the marker text that triggered this boundary
}

// ---------------------------------------------------------------------------
// Regex patterns (7 boundary types by priority)
// ---------------------------------------------------------------------------

// Priority 90: Numbered sections — "1.2.3 Title", "2. Methods", "(1) Overview", "§1"
var numberedSectionPat = regexp.MustCompile(
	`(?m)^(?:\(?\d+(?:\.\d+){0,3}\)?[\.\)\-]?\s+|§\d+\s+|第[一二三四五六七八九十百千\d]+[章节节])\s*\S`)

// Priority 85: Chapter/section keywords (multi-language)
var chapterKeywordPat = regexp.MustCompile(
	`(?mi)^(?:Kapitel|Abschnitt|Teil|Anhang|Chapter|Section|Part|Appendix|第[一二三四五六七八九十百千\d]+[章节])\s`)

// Priority 70: ALL CAPS short heading line (10-80 chars, at least 2 uppercase chars)
var allCapsHeadingPat = regexp.MustCompile(`(?m)^[A-Z][A-Z\s]{4,78}$`)

// Priority 60: Visual separators (3+ repeated punctuation characters on their own line)
var visualSeparatorPat = regexp.MustCompile(`(?m)^[ \t]*([\-=_*#~]{3,})[ \t]*$`)

// Priority 50: Page footers (page number patterns)
var pageFooterPat = regexp.MustCompile(
	`(?mi)(?:^[ \t]*(?:Page|Seite|第)?[ \t]*\d+[ \t]*(?:of|von|页|／|/)[ \t]*\d+[ \t]*$|` +
		`^[ \t]*[-–—]*[ \t]*\d+[ \t]*[-–—]*[ \t]*$)`)

// Priority 40: Excessive blank lines (3+ consecutive newlines)
var excessiveBlanksPat = regexp.MustCompile(`\n{3,}`)

// dedupWindow is the byte distance within which lower-priority markers are
// suppressed in favour of a higher-priority marker.
const dedupWindow = 80

// ---------------------------------------------------------------------------
// Trigger detection
// ---------------------------------------------------------------------------

// hasFormFeed returns true if text contains a form-feed character.
func hasFormFeed(text string) bool {
	return strings.ContainsRune(text, '\f')
}

// shouldUseHeuristic returns true when Tier 2 heuristic splitting should be used.
// Trigger: ≥5 heuristic markers found, or text contains form-feed characters.
func shouldUseHeuristic(text string) bool {
	if hasFormFeed(text) {
		return true
	}
	markers := findAllBoundaryMarkers(text)
	return len(markers) >= 5
}

// ---------------------------------------------------------------------------
// Marker detection
// ---------------------------------------------------------------------------

// findAllBoundaryMarkers scans text for all 7 boundary types and returns a
// position-sorted list (excluding form feed which is handled inline).
// Markers inside protected regions (<figure>...</figure>) are excluded
// to prevent VLM image descriptions from triggering false boundaries.
func findAllBoundaryMarkers(text string) []boundaryMarker {
	// Find protected <figure> spans to exclude from marker detection
	var protectedSpans []span
	for _, m := range regexp.MustCompile(`(?s)<figure>.*?</figure>`).FindAllStringIndex(text, -1) {
		protectedSpans = append(protectedSpans, span{m[0], m[1]})
	}

	// insideProtected returns true if the given byte position falls within
	// a protected <figure> block.
	insideProtected := func(pos int) bool {
		for _, s := range protectedSpans {
			if pos >= s.start && pos < s.end {
				return true
			}
		}
		return false
	}

	var markers []boundaryMarker

	// Priority 100: Form feed — scan manually (not a regex)
	for i := 0; i < len(text); i++ {
		if text[i] == '\f' && !insideProtected(i) {
			markers = append(markers, boundaryMarker{
				priority: boundaryFormFeed,
				position: i,
				length:   1,
				text:     "\f",
			})
		}
	}

	// Priority 90: Numbered sections
	for _, m := range numberedSectionPat.FindAllStringIndex(text, -1) {
		if !insideProtected(m[0]) {
			markers = append(markers, boundaryMarker{
				priority: boundaryNumberedSection,
				position: m[0],
				length:   m[1] - m[0],
				text:     text[m[0]:m[1]],
			})
		}
	}

	// Priority 85: Chapter keywords
	for _, m := range chapterKeywordPat.FindAllStringIndex(text, -1) {
		if !insideProtected(m[0]) {
			markers = append(markers, boundaryMarker{
				priority: boundaryChapterKeyword,
				position: m[0],
				length:   m[1] - m[0],
				text:     text[m[0]:m[1]],
			})
		}
	}

	// Priority 70: All-caps headings
	for _, m := range allCapsHeadingPat.FindAllStringIndex(text, -1) {
		if !insideProtected(m[0]) {
			markers = append(markers, boundaryMarker{
				priority: boundaryAllCapsHeading,
				position: m[0],
				length:   m[1] - m[0],
				text:     text[m[0]:m[1]],
			})
		}
	}

	// Priority 60: Visual separators
	for _, m := range visualSeparatorPat.FindAllStringIndex(text, -1) {
		if !insideProtected(m[0]) {
			markers = append(markers, boundaryMarker{
				priority: boundaryVisualSeparator,
				position: m[0],
				length:   m[1] - m[0],
				text:     text[m[0]:m[1]],
			})
		}
	}

	// Priority 50: Page footers
	for _, m := range pageFooterPat.FindAllStringIndex(text, -1) {
		if !insideProtected(m[0]) {
			markers = append(markers, boundaryMarker{
				priority: boundaryPageFooter,
				position: m[0],
				length:   m[1] - m[0],
				text:     text[m[0]:m[1]],
			})
		}
	}

	// Priority 40: Excessive blank lines
	for _, m := range excessiveBlanksPat.FindAllStringIndex(text, -1) {
		if !insideProtected(m[0]) {
			markers = append(markers, boundaryMarker{
				priority: boundaryExcessiveBlanks,
				position: m[0],
				length:   m[1] - m[0],
				text:     text[m[0]:m[1]],
			})
		}
	}

	// Sort by position ascending, then by priority descending
	sortMarkers(markers)

	return markers
}

// sortMarkers sorts markers by position (primary) and priority desc (secondary).
func sortMarkers(markers []boundaryMarker) {
	for i := 1; i < len(markers); i++ {
		for j := i; j > 0; j-- {
			a, b := markers[j-1], markers[j]
			if a.position > b.position || (a.position == b.position && a.priority < b.priority) {
				markers[j-1], markers[j] = b, a
			} else {
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Deduplication
// ---------------------------------------------------------------------------

// dedupMarkers removes lower-priority markers within dedupWindow bytes of a
// higher-priority marker. Also suppresses markers on the same line.
func dedupMarkers(markers []boundaryMarker, text string) []boundaryMarker {
	if len(markers) <= 1 {
		return markers
	}

	// Build a "suppressed" set by index
	suppressed := make(map[int]bool)

	for i := 0; i < len(markers); i++ {
		if suppressed[i] {
			continue
		}
		hi := markers[i]

		// Suppress lower-priority markers within dedupWindow ahead
		for j := i + 1; j < len(markers); j++ {
			if suppressed[j] {
				continue
			}
			lo := markers[j]
			dist := lo.position - hi.position
			if dist < 0 {
				dist = -dist
			}
			if dist <= dedupWindow {
				// Higher priority suppresses lower; equal priority keeps first
				if hi.priority > lo.priority {
					suppressed[j] = true
				} else if lo.priority > hi.priority {
					suppressed[i] = true
					break // hi was suppressed, stop suppressing with it
				}
			} else {
				break // markers are sorted by position, no need to check further
			}
		}

		// Suppress markers on the same line (between same \n boundaries)
		if !suppressed[i] {
			lineStart := lastIndexOfByte(text, '\n', hi.position) + 1
			lineEnd := indexOfByte(text, '\n', hi.position)
			if lineEnd < 0 {
				lineEnd = len(text)
			}
			for j := i + 1; j < len(markers); j++ {
				if suppressed[j] {
					continue
				}
				if markers[j].position >= lineStart && markers[j].position < lineEnd {
					if hi.priority >= markers[j].priority {
						suppressed[j] = true
					}
				} else {
					break
				}
			}
		}
	}

	out := make([]boundaryMarker, 0, len(markers))
	for i, m := range markers {
		if !suppressed[i] {
			out = append(out, m)
		}
	}
	return out
}

func lastIndexOfByte(s string, c byte, from int) int {
	for i := from; i >= 0; i-- {
		if i < len(s) && s[i] == c {
			return i
		}
	}
	return -1
}

func indexOfByte(s string, c byte, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Section creation from markers
// ---------------------------------------------------------------------------

// markersToHeuristicSections splits text into heuristicSections at the given
// boundary marker positions. Each marker starts a new section (the marker text
// is included at the beginning of the new section's content).
func markersToHeuristicSections(text string, markers []boundaryMarker) []heuristicSection {
	b2r := byteToRuneOffsets(text)

	if len(markers) == 0 {
		// Single section covering the whole text
		runeLen := b2r[len(text)]
		return []heuristicSection{{
			content:      text,
			start:        0,
			end:          runeLen,
			boundaryType: 0,
		}}
	}

	var sections []heuristicSection
	prevBytePos := 0
	var prevBoundary *boundaryMarker

	for i, m := range markers {
		if m.position > prevBytePos {
			// Content before this marker belongs to the previous boundary
			bt := boundaryType(0)
			btText := ""
			if prevBoundary != nil {
				bt = prevBoundary.priority
				btText = prevBoundary.text
			}
			runeStart := b2r[prevBytePos]
			runeEnd := b2r[m.position]
			if runeEnd > runeStart {
				sections = append(sections, heuristicSection{
					content:      text[prevBytePos:m.position],
					start:        runeStart,
					end:          runeEnd,
					boundaryType: bt,
					boundaryText: btText,
				})
			}
		}
		prevBytePos = m.position
		prevBoundary = &markers[i]
	}

	// Final section: from last marker to end of text
	if prevBytePos < len(text) {
		bt := boundaryType(0)
		btText := ""
		if prevBoundary != nil {
			bt = prevBoundary.priority
			btText = prevBoundary.text
		}
		runeStart := b2r[prevBytePos]
		runeEnd := b2r[len(text)]
		if runeEnd > runeStart {
			sections = append(sections, heuristicSection{
				content:      text[prevBytePos:],
				start:        runeStart,
				end:          runeEnd,
				boundaryType: bt,
				boundaryText: btText,
			})
		}
	}

	return sections
}

// ---------------------------------------------------------------------------
// Main entry point (Tier 2)
// ---------------------------------------------------------------------------

// heuristicChunk is the top-level Tier 2 splitting method.
// Phase 1: scan for heuristic boundary markers, deduplicate, split at boundaries.
// Phase 2: oversized sections → chunkFlat.
// Phase 3: tiny chunks → coalesce.
// Phase 4: assemble domain.Chunk with boundary_type metadata.
func (c *RecursiveChunker) heuristicChunk(doc *domain.ParsedDocument, cfg domain.ChunkingConfig) ([]domain.Chunk, error) {
	chunkSize := normalizeChunkSize(cfg.ChunkSize)
	overlap := normalizeOverlap(cfg.Overlap, chunkSize)
	separators := normalizeSeparators(cfg.Separators)
	tinyThreshold := tinyChunkThreshold(chunkSize)

	// Phase 1: Find markers, dedup, split into sections
	markers := findAllBoundaryMarkers(doc.RawText)
	markers = dedupMarkers(markers, doc.RawText)
	sections := markersToHeuristicSections(doc.RawText, markers)

	// Phase 2: Split oversized sections
	flatSections, err := c.splitOversizedHeuristicSections(sections, chunkSize, overlap, separators)
	if err != nil {
		return nil, err
	}

	// Phase 3: Coalesce tiny adjacent chunks
	merged := coalesceTinyHeuristicChunks(flatSections, tinyThreshold)

	// Phase 4: Assemble final chunks
	return assembleHeuristicChunks(merged), nil
}

// heuristicParentChild produces parent-child chunks with heuristic splitting
// for parent generation and flat splitting for children.
func (c *RecursiveChunker) heuristicParentChild(doc *domain.ParsedDocument, cfg domain.ChunkingConfig) (*domain.ParentChildChunks, error) {
	separators := normalizeSeparators(cfg.Separators)

	parentSize := cfg.ParentChild.ParentSize
	if parentSize <= 0 {
		parentSize = 4096
	}
	childSize := cfg.ParentChild.ChildSize
	if childSize <= 0 {
		childSize = 384
	}

	// Parents: heuristic split with parentSize
	parentCfg := cfg
	parentCfg.ChunkSize = parentSize
	parentCfg.Overlap = 0
	parentCfg.ParentChild.Enabled = false
	parents, err := c.heuristicChunk(doc, parentCfg)
	if err != nil {
		return nil, err
	}

	// Children: flat split within each parent
	childCfg := domain.ChunkingConfig{
		ChunkSize:  childSize,
		Overlap:    childSize / 5,
		Separators: separators,
	}

	var result []domain.Chunk
	var children []domain.Chunk
	childIdx := 0
	for pi := range parents {
		parents[pi].Metadata["block_type"] = "parent"
		result = append(result, parents[pi])

		subDoc := &domain.ParsedDocument{RawText: parents[pi].Content}
		subChildren, err := c.chunkFlat(subDoc, childCfg)
		if err != nil {
			return nil, err
		}
		for ci := range subChildren {
			ch := &subChildren[ci]
			ch.Index = childIdx
			if ch.Metadata == nil {
				ch.Metadata = make(map[string]any)
			}
			ch.Metadata["block_type"] = "child"
			ch.Metadata["parent_index"] = pi
			// Inherit boundary metadata from parent
			if bt, ok := parents[pi].Metadata["boundary_type"]; ok {
				ch.Metadata["boundary_type"] = bt
			}
			if bt, ok := parents[pi].Metadata["boundary_text"]; ok {
				ch.Metadata["boundary_text"] = bt
			}
			ch.StartPos = parents[pi].StartPos + ch.StartPos
			ch.EndPos = parents[pi].StartPos + ch.EndPos
			childIdx++
		}
		children = append(children, subChildren...)
	}
	return &domain.ParentChildChunks{Parents: result, Children: children}, nil
}

// ---------------------------------------------------------------------------
// Phase 2: Oversized section splitting
// ---------------------------------------------------------------------------

func (c *RecursiveChunker) splitOversizedHeuristicSections(
	sections []heuristicSection,
	chunkSize, overlap int,
	separators []string,
) ([]heuristicSection, error) {
	out := make([]heuristicSection, 0, len(sections))
	for _, sec := range sections {
		runeLen := utf8.RuneCountInString(sec.content)
		if runeLen <= chunkSize {
			out = append(out, sec)
			continue
		}

		subDoc := &domain.ParsedDocument{RawText: sec.content}
		subCfg := domain.ChunkingConfig{
			ChunkSize:  chunkSize,
			Overlap:    overlap,
			Separators: separators,
		}
		subChunks, err := c.chunkFlat(subDoc, subCfg)
		if err != nil {
			return nil, err
		}

		for _, ch := range subChunks {
			out = append(out, heuristicSection{
				content:      ch.Content,
				start:        sec.start + ch.StartPos,
				end:          sec.start + ch.EndPos,
				boundaryType: 0, // sub-divisions are not original boundaries
				boundaryText: sec.boundaryText,
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Phase 3: Tiny chunk coalescing
// ---------------------------------------------------------------------------

func coalesceTinyHeuristicChunks(chunks []heuristicSection, threshold int) []heuristicSection {
	if len(chunks) <= 1 {
		return chunks
	}

	out := make([]heuristicSection, 0, len(chunks))
	i := 0
	for i < len(chunks) {
		current := chunks[i]
		runeLen := utf8.RuneCountInString(current.content)

		j := i + 1
		// Only merge sub-divisions (boundaryType==0), never merge original boundary sections.
		for runeLen < threshold && j < len(chunks) && current.boundaryType == 0 {
			next := chunks[j]
			if next.boundaryType != 0 {
				break
			}
			combined := current.content + next.content
			current = heuristicSection{
				content:      combined,
				start:        current.start,
				end:          next.end,
				boundaryType: 0,
				boundaryText: current.boundaryText,
			}
			runeLen = utf8.RuneCountInString(combined)
			j++
		}
		out = append(out, current)
		i = j
	}
	return out
}

// ---------------------------------------------------------------------------
// Phase 4: Assemble domain.Chunk
// ---------------------------------------------------------------------------

func assembleHeuristicChunks(sections []heuristicSection) []domain.Chunk {
	chunks := make([]domain.Chunk, len(sections))
	for i, sec := range sections {
		meta := make(map[string]any)
		if sec.boundaryType > 0 {
			meta["boundary_type"] = boundaryTypeName(sec.boundaryType)
		}
		if sec.boundaryText != "" {
			meta["boundary_text"] = strings.TrimSpace(sec.boundaryText)
		}
		chunks[i] = domain.Chunk{
			Index:      i,
			Content:    sec.content,
			TokenCount: estimateTokens(sec.content),
			StartPos:   sec.start,
			EndPos:     sec.end,
			Metadata:   meta,
		}
	}
	return chunks
}

func boundaryTypeName(bt boundaryType) string {
	switch bt {
	case boundaryFormFeed:
		return "form_feed"
	case boundaryNumberedSection:
		return "numbered_section"
	case boundaryChapterKeyword:
		return "chapter_keyword"
	case boundaryAllCapsHeading:
		return "all_caps_heading"
	case boundaryVisualSeparator:
		return "visual_separator"
	case boundaryPageFooter:
		return "page_footer"
	case boundaryExcessiveBlanks:
		return "excessive_blanks"
	default:
		return "unknown"
	}
}
