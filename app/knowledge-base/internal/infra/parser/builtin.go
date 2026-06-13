package parser

import (
	"bytes"
	"context"
	"strings"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

// BuiltinParser handles text-based file formats: txt, md, html, json, xml, csv, yaml, yml.
// Format-specific conversion happens in convertToText before markdown heading detection.
type BuiltinParser struct {
	fileType string
}

// NewBuiltinParser creates a builtin parser for the given file type.
// fileType should be the lowercase file extension without the dot (e.g. "html", "json", "csv").
func NewBuiltinParser(fileType string) *BuiltinParser {
	return &BuiltinParser{fileType: fileType}
}

func (p *BuiltinParser) Name() string {
	return "builtin"
}

// Parse converts the raw bytes to text according to the file type, then
// detects markdown heading sections, language, and page structure.
func (p *BuiltinParser) Parse(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	// Step 1: Convert format-specific content to text/markdown
	text := convertToText(raw, p.fileType)

	doc := &domain.ParsedDocument{
		RawText: text,
		Metadata: domain.DocumentMeta{
			Language: detectLanguage(text),
		},
		Pages: []domain.PageText{
			{PageNum: 1, Text: text},
		},
	}

	// Step 2: Detect markdown heading sections
	lines := strings.Split(text, "\n")
	var sections []domain.Section
	var currentSection *domain.Section
	pos := 0

	for _, line := range lines {
		lineLen := len(line) + 1
		if isHeading(line) {
			if currentSection != nil {
				currentSection.EndPos = pos
				sections = append(sections, *currentSection)
			}
			level := headingLevel(line)
			title := strings.TrimSpace(strings.TrimLeft(line, "# "))
			currentSection = &domain.Section{
				Title:    title,
				Level:    level,
				Content:  line,
				PageNum:  1,
				StartPos: pos,
			}
		} else if currentSection != nil {
			currentSection.Content += "\n" + line
		} else {
			currentSection = &domain.Section{
				Title:    "",
				Level:    0,
				Content:  line,
				PageNum:  1,
				StartPos: 0,
			}
		}
		pos += lineLen
	}
	if currentSection != nil {
		currentSection.EndPos = pos
		sections = append(sections, *currentSection)
	}
	doc.Sections = sections

	return doc, nil
}

func isHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return false
	}
	for _, c := range trimmed {
		if c == '#' {
			continue
		}
		return c == ' '
	}
	return false
}

func headingLevel(line string) int {
	trimmed := strings.TrimSpace(line)
	for i, c := range trimmed {
		if c != '#' {
			return i
		}
	}
	return 1
}

func detectLanguage(text string) string {
	if len(text) == 0 {
		return "unknown"
	}
	nonASCII := 0
	total := 0
	for _, r := range text {
		if r > 127 {
			nonASCII++
		}
		total++
	}
	if total > 0 && float64(nonASCII)/float64(total) > 0.1 {
		return "zh"
	}
	return "en"
}

func isBinary(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	return len(data) > 0 && bytes.Contains(data[:checkLen], []byte{0})
}
