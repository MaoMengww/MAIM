package chunker

import (
	"strings"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

// HeadingNode represents one level in the heading breadcrumb hierarchy.
type HeadingNode struct {
	Title string `json:"title"`
	Level int    `json:"level"`
}

// headingSection is an intermediate chunk produced by heading-based splitting.
// It carries the heading breadcrumb path that contextualizes the content.
type headingSection struct {
	content    string
	start, end int           // rune offsets in the original document
	breadcrumb []HeadingNode // heading hierarchy path (shallow copy of stack)
}

// byteToRuneOffsets builds a mapping from byte positions to rune positions.
// offsets[bytePos] returns the rune offset at that byte position.
// The returned slice has length len(text)+1; the last element is total rune count.
func byteToRuneOffsets(text string) []int {
	offsets := make([]int, len(text)+1)
	runePos := 0
	bytePos := 0
	for bytePos < len(text) {
		offsets[bytePos] = runePos
		_, size := utf8.DecodeRuneInString(text[bytePos:])
		if size <= 0 {
			break
		}
		bytePos += size
		runePos++
	}
	offsets[bytePos] = runePos
	// Fill any remaining positions (shouldn't happen, but ensures monotonicity)
	for i := bytePos + 1; i <= len(text); i++ {
		offsets[i] = runePos
	}
	return offsets
}

// hasRealHeadings returns true if any section has Level > 0 (a real markdown heading).
func hasRealHeadings(sections []domain.Section) bool {
	for _, s := range sections {
		if s.Level > 0 {
			return true
		}
	}
	return false
}

// headingSectionsFromText scans text for markdown headings directly,
// without relying on parser Section byte positions (which may be stale
// after RawText modifications like VLM image replacements).
func headingSectionsFromText(text string) []headingSection {
	b2r := byteToRuneOffsets(text)
	stack := make([]HeadingNode, 0, 4)
	type headingPos struct {
		title   string
		level   int
		bytePos int
	}
	var headings []headingPos

	lines := strings.Split(text, "\n")
	bytePos := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for _, ch := range trimmed {
				if ch == '#' {
					level++
				} else {
					break
				}
			}
			if level > 0 && len(trimmed) > level && trimmed[level] == ' ' {
				title := strings.TrimSpace(trimmed[level:])
				headings = append(headings, headingPos{title: title, level: level, bytePos: bytePos})
			}
		}
		bytePos += len(line) + 1
	}

	out := make([]headingSection, 0, len(headings))
	for i, h := range headings {
		endByte := len(text)
		if i+1 < len(headings) {
			endByte = headings[i+1].bytePos
		}
		for len(stack) > 0 && stack[len(stack)-1].Level >= h.level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, HeadingNode{Title: h.title, Level: h.level})
		bc := make([]HeadingNode, len(stack))
		copy(bc, stack)
		out = append(out, headingSection{
			content:    text[h.bytePos:endByte],
			start:      b2r[h.bytePos],
			end:        b2r[endByte],
			breadcrumb: bc,
		})
	}
	if len(headings) > 0 && headings[0].bytePos > 0 {
		prefix := text[:headings[0].bytePos]
		if len(strings.TrimSpace(prefix)) > 0 {
			out = append([]headingSection{{
				content:    prefix,
				start:      0,
				end:        b2r[headings[0].bytePos],
				breadcrumb: nil,
			}}, out...)
		}
	}
	return out
}

// headingAwareChunk is the top-level Tier 1 splitting method.
// Phase 1: split at heading boundaries with breadcrumb hierarchy.
// Phase 2: oversized sections → chunkFlat.
// Phase 3: tiny chunks → coalesce.
// Phase 4: assemble domain.Chunk with heading_path metadata.
func (c *RecursiveChunker) headingAwareChunk(doc *domain.ParsedDocument, cfg domain.ChunkingConfig) ([]domain.Chunk, error) {
	chunkSize := normalizeChunkSize(cfg.ChunkSize)
	overlap := normalizeOverlap(cfg.Overlap, chunkSize)
	separators := normalizeSeparators(cfg.Separators)
	tinyThreshold := tinyChunkThreshold(chunkSize)

	// Phase 1: Split at heading boundaries
	sections := headingSectionsFromText(doc.RawText)

	// Phase 2: Split oversized sections using chunkFlat
	flatSections, err := c.splitOversizedSections(sections, chunkSize, overlap, separators)
	if err != nil {
		return nil, err
	}

	// Phase 3: Coalesce tiny adjacent chunks
	merged := coalesceTinyChunks(flatSections, tinyThreshold)

	// Phase 4: Assemble final chunks
	return assembleChunks(merged), nil
}

// headingAwareParentChild produces parent-child chunks using heading-aware
// splitting for parent generation and flat splitting for children.
func (c *RecursiveChunker) headingAwareParentChild(doc *domain.ParsedDocument, cfg domain.ChunkingConfig) (*domain.ParentChildChunks, error) {
	separators := normalizeSeparators(cfg.Separators)

	parentSize := cfg.ParentChild.ParentSize
	if parentSize <= 0 {
		parentSize = 4096
	}
	childSize := cfg.ParentChild.ChildSize
	if childSize <= 0 {
		childSize = 384
	}

	// Parents: heading-aware split with parentSize
	parentCfg := cfg
	parentCfg.ChunkSize = parentSize
	parentCfg.Overlap = 0
	parentCfg.ParentChild.Enabled = false // prevent recursion
	parents, err := c.headingAwareChunk(doc, parentCfg)
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
			// Inherit heading_path from parent
			if hp, ok := parents[pi].Metadata["heading_path"]; ok {
				ch.Metadata["heading_path"] = hp
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

// splitOversizedSections delegates sections exceeding chunkSize to chunkFlat.
// Sub-chunks inherit the parent section's breadcrumb.
func (c *RecursiveChunker) splitOversizedSections(
	sections []headingSection,
	chunkSize, overlap int,
	separators []string,
) ([]headingSection, error) {
	out := make([]headingSection, 0, len(sections))
	for _, sec := range sections {
		runeLen := utf8.RuneCountInString(sec.content)
		if runeLen <= chunkSize {
			out = append(out, sec)
			continue
		}

		// Delegate to existing recursive chunker
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
			out = append(out, headingSection{
				content:    ch.Content,
				start:      sec.start + ch.StartPos,
				end:        sec.start + ch.EndPos,
				breadcrumb: sec.breadcrumb, // inherit parent's breadcrumb
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Phase 3: Tiny chunk coalescing
// ---------------------------------------------------------------------------

// coalesceTinyChunks merges adjacent chunks smaller than threshold.
// Merging only occurs between chunks with identical breadcrumb paths
// (same-section siblings).
func coalesceTinyChunks(chunks []headingSection, threshold int) []headingSection {
	if len(chunks) <= 1 {
		return chunks
	}

	out := make([]headingSection, 0, len(chunks))
	i := 0
	for i < len(chunks) {
		current := chunks[i]
		runeLen := utf8.RuneCountInString(current.content)

		// Try to merge with next chunk(s) while current is below threshold
		j := i + 1
		for runeLen < threshold && j < len(chunks) {
			next := chunks[j]
			if !breadcrumbsEqual(current.breadcrumb, next.breadcrumb) {
				break
			}
			combined := current.content + next.content
			current = headingSection{
				content:    combined,
				start:      current.start,
				end:        next.end,
				breadcrumb: current.breadcrumb,
			}
			runeLen = utf8.RuneCountInString(combined)
			j++
		}
		out = append(out, current)
		i = j
	}
	return out
}

func breadcrumbsEqual(a, b []HeadingNode) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Level != b[i].Level || a[i].Title != b[i].Title {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Phase 4: Assemble domain.Chunk
// ---------------------------------------------------------------------------

// assembleChunks converts headingSections to domain.Chunk with breadcrumb metadata.
func assembleChunks(sections []headingSection) []domain.Chunk {
	chunks := make([]domain.Chunk, len(sections))
	for i, sec := range sections {
		meta := make(map[string]any)
		nodes := make([]map[string]any, len(sec.breadcrumb))
		for j, n := range sec.breadcrumb {
			nodes[j] = map[string]any{
				"level": n.Level,
				"title": n.Title,
			}
		}
		meta["heading_path"] = nodes
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

// ---------------------------------------------------------------------------
// Normalization helpers
// ---------------------------------------------------------------------------

func normalizeChunkSize(sz int) int {
	if sz <= 0 {
		return 512
	}
	return sz
}

func normalizeOverlap(ov, chunkSize int) int {
	if ov <= 0 {
		return 50
	}
	if ov >= chunkSize {
		return chunkSize / 2
	}
	return ov
}

func normalizeSeparators(seps []string) []string {
	if len(seps) == 0 {
		return []string{"\n\n", "\n", "。"}
	}
	return seps
}

func tinyChunkThreshold(chunkSize int) int {
	t := chunkSize / 4
	if t < 50 {
		t = 50
	}
	return t
}
