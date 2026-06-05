package domain

import "context"

type ImageRef struct {
	Index       int
	URL         string // original source URL (for Markdown images)
	RawContent  []byte
	ContentType string
	AltText     string
	MinioKey    string
}

type ParsedDocument struct {
	RawText  string
	Images   []ImageRef
	Sections []Section
	Metadata DocumentMeta
	Pages    []PageText
}

type Section struct {
	Title    string
	Level    int
	Content  string
	PageNum  int
	StartPos int
	EndPos   int
}

type DocumentMeta struct {
	Title     string
	Author    string
	PageCount int
	Language  string
	Extra     map[string]string
}

type PageText struct {
	PageNum int
	Text    string
}

type Parser interface {
	Parse(ctx context.Context, raw []byte) (*ParsedDocument, error)
	Name() string
}
