package chunker

import (
	"regexp"
	"strings"
)

// headerTrackerHook defines how to detect a header region (start/end
// patterns) and its relative priority among other hooks.
type headerTrackerHook struct {
	priority     int
	startPattern *regexp.Regexp
	endPattern   *regexp.Regexp
}

// headerTracker marks the active table header so the merge logic can
// prepend column names to chunks that span beyond the first table row.
type headerTracker struct {
	hooks         []headerTrackerHook
	activeHeaders map[int]string
	endedHeaders  map[int]bool
	pendingExtend map[int]bool
}

var (
	tableStartPat = regexp.MustCompile(`(?si)^\s*(?:\|[^|\n]*)+[\r\n]+\s*(?:\|\s*:?-{3,}:?\s*)+\|?[\r\n]+$`)
	tableEndPat   = regexp.MustCompile(`(?si)^\s*$|^\s*[^|\s].*$`)
	tableRowPat   = regexp.MustCompile(`(?m)^\s*(?:\|[^|\n]*)+\|\s*$`)
)

var defaultHeaderHooks = []headerTrackerHook{
	{
		priority:     0,
		startPattern: tableStartPat,
		endPattern:   tableEndPat,
	},
}

func newHeaderTracker() *headerTracker {
	return &headerTracker{
		hooks:         defaultHeaderHooks,
		activeHeaders: make(map[int]string),
		endedHeaders:  make(map[int]bool),
		pendingExtend: make(map[int]bool),
	}
}

// update checks whether the split unit starts or ends a table header.
func (ht *headerTracker) update(split string) {
	// Check for header-end markers
	for _, hook := range ht.hooks {
		if _, active := ht.activeHeaders[hook.priority]; active && hook.endPattern.MatchString(split) {
			delete(ht.activeHeaders, hook.priority)
			ht.endedHeaders[hook.priority] = true
		}
	}

	// Reset ended flag BEFORE checking start patterns, so a subsequent table
	// in the same document can be detected.
	for _, hook := range ht.hooks {
		if ht.endedHeaders[hook.priority] && !hook.endPattern.MatchString(split) {
			delete(ht.endedHeaders, hook.priority)
		}
	}

	// If a header has an empty column-name row (e.g. "||"), replace it with
	// a proper Markdown table header using the first data row as column names.
	for p := range ht.pendingExtend {
		if _, active := ht.activeHeaders[p]; active && tableRowPat.MatchString(split) {
			sep := extractSeparatorLine(ht.activeHeaders[p])
			ht.activeHeaders[p] = split + sep
		}
		delete(ht.pendingExtend, p)
	}

	// Check for new header-start markers
	for _, hook := range ht.hooks {
		if !ht.endedHeaders[hook.priority] {
			if loc := hook.startPattern.FindString(split); loc != "" {
				ht.activeHeaders[hook.priority] = loc
				if isEmptyTableHeaderRow(loc) {
					ht.pendingExtend[hook.priority] = true
				}
			}
		}
	}
}

func (ht *headerTracker) getHeaders() string {
	var result string
	for _, hook := range ht.hooks {
		if h, ok := ht.activeHeaders[hook.priority]; ok {
			result += h
		}
	}
	return result
}

func isEmptyTableRow(header string) bool {
	idx := strings.IndexByte(header, '\n')
	if idx < 0 {
		return false
	}
	row := strings.TrimSpace(header[:idx])
	for _, r := range row {
		if r != '|' && r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

func extractSepLine(header string) string {
	for _, line := range strings.Split(header, "\n") {
		if strings.Contains(line, "---") {
			return "\n" + line + "\n"
		}
	}
	return "\n"
}

func isEmptyTableHeaderRow(header string) bool {
	idx := strings.IndexByte(header, '\n')
	if idx < 0 {
		return false
	}
	row := strings.TrimSpace(header[:idx])
	for _, r := range row {
		if r != '|' && r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

func extractSeparatorLine(header string) string {
	for _, line := range strings.Split(header, "\n") {
		if strings.Contains(line, "---") {
			return line + "\n"
		}
	}
	return ""
}

func headerColumnRow(header string) string {
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "---") {
			continue
		}
		onlyPipes := true
		for _, r := range line {
			if r != '|' && r != ' ' && r != '\t' {
				onlyPipes = false
				break
			}
		}
		if !onlyPipes {
			return line
		}
	}
	return ""
}

// columnNames extracts the first meaningful (non-separator) row from headers.
func columnNames(headers string) string {
	for _, line := range strings.Split(headers, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "---") {
			continue
		}
		onlyPipes := true
		for _, r := range line {
			if r != '|' && r != ' ' && r != '\t' {
				onlyPipes = false
				break
			}
		}
		if !onlyPipes {
			return line
		}
	}
	return ""
}

// headerAlreadyPresent checks if the table header's column-name row already
// appears in text, to avoid duplicating it when prepending.
func headerAlreadyPresent(headers, overlapText, unitText string) bool {
	if strings.Contains(overlapText, headers) || strings.Contains(unitText, headers) {
		return true
	}
	colRow := headerColumnRow(headers)
	if colRow == "" {
		return false
	}
	return strings.Contains(overlapText, colRow) || strings.Contains(unitText, colRow)
}
