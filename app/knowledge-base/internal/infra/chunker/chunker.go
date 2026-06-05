package chunker

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

// RecursiveChunker recursively splits text using a priority-ordered list of
// separators, while preserving the integrity of protected blocks (code fences,
// LaTeX math, Markdown tables, images, links).
type RecursiveChunker struct{}

func NewChunker() *RecursiveChunker {
	return &RecursiveChunker{}
}

var protectedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^#{1,6}\s+.*$`),                                                             // Markdown headings
	regexp.MustCompile("(?s)```(?:\\w+)?[\\r\\n].*?```"),                                                // Fenced code
	regexp.MustCompile(`(?s)\$\$.*?\$\$`),                                                               // LaTeX block math
	regexp.MustCompile("(?m)[ ]*(?:\\|[^|\\n]*)+\\|[\\r\\n]+\\s*(?:\\|\\s*:?-{3,}:?\\s*)+\\|[\\r\\n]+"), // Table header+separator
	regexp.MustCompile("(?m)[ ]*(?:\\|[^|\\n]*)+\\|[\\r\\n]+"),                                          // Table rows
	regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`),                                                          // Images
	regexp.MustCompile(`\[[^\]]*\]\([^)]+\)`),                                                           // Links
	regexp.MustCompile(`(?s)<figure>.*?</figure>`),                                                      // VLM descriptions (protected)
}

const maxProtectedSize = 7500

type span struct{ start, end int }

type splitUnit struct {
	text       string
	start, end int // rune offsets in the original document
}

func (c *RecursiveChunker) Chunk(ctx context.Context, doc *domain.ParsedDocument, cfg domain.ChunkingConfig) (*domain.ParentChildChunks, error) {
	if cfg.ParentChild.Enabled {
		return c.chunkWithParent(doc, cfg)
	}
	chunks, err := c.chunkFlat(doc, cfg)
	if err != nil {
		return nil, err
	}
	return &domain.ParentChildChunks{Children: chunks}, nil
}

// chunkFlat implements single-level chunking with protection and header context.
func (c *RecursiveChunker) chunkFlat(doc *domain.ParsedDocument, cfg domain.ChunkingConfig) ([]domain.Chunk, error) {
	separators := cfg.Separators
	if len(separators) == 0 {
		separators = []string{"\n\n", "\n", "。"}
	}
	chunkSize := cfg.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 512
	}
	overlap := cfg.Overlap
	if overlap <= 0 {
		overlap = 50
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 2
	}

	text := doc.RawText
	units := buildUnits(text, findProtectedSpans(text), separators)
	merged := mergeUnits(units, chunkSize, overlap, separators)

	result := make([]domain.Chunk, len(merged))
	for i, ck := range merged {
		result[i] = domain.Chunk{
			Index:      i,
			Content:    ck.Content,
			TokenCount: estimateTokens(ck.Content),
			StartPos:   ck.Start,
			EndPos:     ck.End,
			Metadata:   ck.Metadata,
		}
	}
	return result, nil
}

// findProtectedSpans locates non-overlapping protected regions.
func findProtectedSpans(text string) []span {
	type item struct{ start, end int }
	var all []item
	for _, pat := range protectedPatterns {
		for _, loc := range pat.FindAllStringIndex(text, -1) {
			if loc[1]-loc[0] > 0 {
				all = append(all, item{loc[0], loc[1]})
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	// Sort by start asc, then by length desc (longer spans win ties).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0; j-- {
			a, b := all[j], all[j-1]
			if a.start < b.start || (a.start == b.start && a.end-a.start > b.end-b.start) {
				all[j], all[j-1] = all[j-1], all[j]
			} else {
				break
			}
		}
	}
	out := make([]span, 0, len(all))
	last := 0
	for _, it := range all {
		if it.start >= last {
			out = append(out, span{it.start, it.end})
			last = it.end
		}
	}
	return out
}

// splitBySeparators splits text by the given separators, retaining the
// separators themselves as separate elements in the output so they stay
// with the preceding text when merged downstream.
func splitBySeparators(text string, separators []string) []string {
	if len(separators) == 0 || text == "" {
		return []string{text}
	}
	var parts []string
	for _, sep := range separators {
		parts = append(parts, regexp.QuoteMeta(sep))
	}
	re := regexp.MustCompile("(" + strings.Join(parts, "|") + ")")
	splits := re.Split(text, -1)
	matches := re.FindAllString(text, -1)

	out := make([]string, 0, len(splits)+len(matches))
	for i, s := range splits {
		if s != "" {
			out = append(out, s)
		}
		if i < len(matches) && matches[i] != "" {
			out = append(out, matches[i])
		}
	}
	return out
}

// buildUnits chops text into fine-grained split units while keeping protected
// ranges atomic. All offsets are rune-based.
func buildUnits(text string, protected []span, separators []string) []splitUnit {
	var units []splitUnit
	bytePos := 0
	runePos := 0

	flush := func(s string, roff int) {
		for _, part := range splitBySeparators(s, separators) {
			rl := runeLen(part)
			units = append(units, splitUnit{text: part, start: roff, end: roff + rl})
			roff += rl
		}
	}

	for _, p := range protected {
		if p.start > bytePos {
			flush(text[bytePos:p.start], runePos)
			runePos += runeLen(text[bytePos:p.start])
		}
		raw := text[p.start:p.end]
		rl := runeLen(raw)
		if rl > maxProtectedSize {
			runes := []rune(raw)
			off := 0
			for off < len(runes) {
				ce := off + maxProtectedSize
				if ce > len(runes) {
					ce = len(runes)
				} else {
					for i := ce - 1; i > off && i > ce-200; i-- {
						if runes[i] == '\n' || runes[i] == ' ' {
							ce = i + 1
							break
						}
					}
				}
				chunk := string(runes[off:ce])
				cl := ce - off
				units = append(units, splitUnit{text: chunk, start: runePos + off, end: runePos + off + cl})
				off = ce
			}
		} else {
			units = append(units, splitUnit{text: raw, start: runePos, end: runePos + rl})
		}
		runePos += rl
		bytePos = p.end
	}
	if bytePos < len(text) {
		flush(text[bytePos:], runePos)
	}
	return units
}

// rawChunk is an intermediate result before conversion to domain.Chunk.
type rawChunk struct {
	Content  string
	Seq      int
	Start    int
	End      int
	Metadata map[string]any
}

// mergeUnits combines split units into chunks of approximately chunkSize runes.
// It tracks table headers and prepends them to continuation chunks so that
// column context is never lost. Overlap is taken from the tail of the most
// recent chunk at flush time. Separator-only units at the tail are excluded
// from overlap so they don't appear at the start of the next chunk.
func mergeUnits(units []splitUnit, chunkSize, overlap int, separators []string) []rawChunk {
	if len(units) == 0 {
		return nil
	}

	ht := newHeaderTracker()
	var chunks []rawChunk
	var cur []splitUnit
	curLen := 0

	// flush builds a rawChunk from cur and returns the overlap tail.
	flush := func() ([]splitUnit, int) {
		if len(cur) == 0 {
			return nil, 0
		}
		var sb strings.Builder
		for _, u := range cur {
			sb.WriteString(u.text)
		}
		chunks = append(chunks, rawChunk{
			Content: sb.String(),
			Seq:     len(chunks),
			Start:   cur[0].start,
			End:     cur[len(cur)-1].end,
		})
		// Compute overlap from the tail of cur before clearing it.
		var tail []splitUnit
		tailLen := 0
		if overlap > 0 {
			for i := len(cur) - 1; i >= 0; i-- {
				uLen := runeLen(cur[i].text)
				if tailLen+uLen > overlap {
					break
				}
				tail = append([]splitUnit{cur[i]}, tail...)
				tailLen += uLen
			}
			// Strip leading separator-only units from the overlap tail
			// so they don't appear at the start of the next chunk.
			for len(tail) > 0 {
				t := strings.TrimSpace(tail[0].text)
				if t != "" && t != "。" && t != "！" && t != "？" {
					break
				}
				tail = tail[1:]
			}
			if len(tail) > 0 {
				tailLen = 0
				for _, u := range tail {
					tailLen += runeLen(u.text)
				}
			}
		}

		cur = nil
		curLen = 0
		return tail, tailLen
	}

	for _, u := range units {
		uLen := runeLen(u.text)

		// Oversized unit — flush current, then force-split it into pieces.
		if uLen > maxProtectedSize {
			flush()
			ht.update(u.text)
			runes := []rune(u.text)
			off := 0
			for off < len(runes) {
				ce := off + maxProtectedSize
				if ce > len(runes) {
					ce = len(runes)
				} else {
					for i := ce - 1; i > off && i > ce-200; i-- {
						if runes[i] == '\n' || runes[i] == ' ' {
							ce = i + 1
							break
						}
					}
				}
				sub := string(runes[off:ce])
				chunks = append(chunks, rawChunk{
					Content: sub,
					Seq:     len(chunks),
					Start:   u.start + off,
					End:     u.start + ce,
				})
				off = ce
			}
			continue
		}

		ht.update(u.text)
		hdrs := ht.getHeaders()

		// If this unit pushes us past chunk size, flush and set up overlap.
		if curLen+uLen > chunkSize && len(cur) > 0 {
			tail, tailLen := flush()
			cur, curLen = tail, tailLen
		}

		// Prepend table headers if needed and they fit.
		if hdrs != "" && !headerAlreadyPresent(hdrs, unitsText(cur), u.text) {
			hl := runeLen(hdrs)
			if curLen+uLen+hl <= chunkSize || (uLen+hl <= chunkSize) {
				hdrUnit := splitUnit{text: hdrs, start: u.start, end: u.start}
				cur = append([]splitUnit{hdrUnit}, cur...)
				curLen += hl
			}
		}

		// Enforce absolute max size — trim from front until u fits.
		for curLen+uLen > maxProtectedSize && len(cur) > 0 {
			curLen -= runeLen(cur[0].text)
			cur = cur[1:]
		}

		cur = append(cur, u)
		curLen += uLen
	}

	// Flush remaining.
	if len(cur) > 0 {
		flush()
	}
	return chunks
}

// chunkWithParent produces two-level parent-child chunks.
// Parent = large window for LLM context; Child = small window for retrieval.
// Parent and child configs are fully independent of the base ChunkSize.
func (c *RecursiveChunker) chunkWithParent(doc *domain.ParsedDocument, cfg domain.ChunkingConfig) (*domain.ParentChildChunks, error) {
	separators := cfg.Separators
	if len(separators) == 0 {
		separators = []string{"\n\n", "\n", "。"}
	}

	parentSize := cfg.ParentChild.ParentSize
	if parentSize <= 0 {
		parentSize = 4096
	}

	childSize := cfg.ParentChild.ChildSize
	if childSize <= 0 {
		childSize = 384
	}

	parentCfg := domain.ChunkingConfig{
		ChunkSize:  parentSize,
		Overlap:    0,
		Separators: separators,
	}
	childCfg := domain.ChunkingConfig{
		ChunkSize:  childSize,
		Overlap:    childSize / 5,
		Separators: separators,
	}

	parents, err := c.chunkFlat(doc, parentCfg)
	if err != nil {
		return nil, err
	}

	var result []domain.Chunk
	var children []domain.Chunk
	childIdx := 0
	for pi := range parents {
		subDoc := &domain.ParsedDocument{RawText: parents[pi].Content}
		subChildren, err := c.chunkFlat(subDoc, childCfg)
		if err != nil {
			return nil, err
		}

		// Set parent metadata
		parents[pi].Metadata = map[string]any{"block_type": "parent"}
		result = append(result, parents[pi])

		for ci := range subChildren {
			ch := &subChildren[ci]
			ch.Index = childIdx
			if ch.Metadata == nil {
				ch.Metadata = make(map[string]any)
			}
			ch.Metadata["block_type"] = "child"
			ch.Metadata["parent_index"] = pi
			ch.StartPos = parents[pi].StartPos + ch.StartPos
			ch.EndPos = parents[pi].StartPos + ch.EndPos
			childIdx++
		}
		children = append(children, subChildren...)
	}

	return &domain.ParentChildChunks{Parents: result, Children: children}, nil
}

// -- helpers --

func unitsText(u []splitUnit) string {
	var sb strings.Builder
	for _, v := range u {
		sb.WriteString(v.text)
	}
	return sb.String()
}

func estimateTokens(s string) int {
	return utf8.RuneCountInString(s) * 4 / 3
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
