package parser

import (
	"bytes"
	"context"
	"strings"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

type BuiltinParser struct{}

func NewBuiltinParser() *BuiltinParser {
	return &BuiltinParser{}
}

func (p *BuiltinParser) Name() string {
	return "builtin"
}

func (p *BuiltinParser) Parse(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	text := string(raw)

	doc := &domain.ParsedDocument{
		RawText: text,
		Metadata: domain.DocumentMeta{
			Language: detectLanguage(text),
		},
		Pages: []domain.PageText{
			{PageNum: 1, Text: text},
		},
	}

	lines := strings.Split(text, "\n")
	var sections []domain.Section
	var currentSection *domain.Section
	pos := 0

	for i, line := range lines {
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
		_ = i
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
	return len(data) > 0 && bytes.Contains(data[:min(len(data), 512)], []byte{0})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
