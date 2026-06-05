package parser

import (
	"context"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
	"github.com/maomeng/aim/pkg/errors"
)

type ParserChain struct {
	parsers []domain.Parser
}

func NewParserChain(parsers ...domain.Parser) *ParserChain {
	return &ParserChain{parsers: parsers}
}

func (c *ParserChain) Name() string {
	return "chain"
}

func (c *ParserChain) Parse(ctx context.Context, raw []byte) (*domain.ParsedDocument, error) {
	var lastErr error
	for _, p := range c.parsers {
		doc, err := p.Parse(ctx, raw)
		if err == nil {
			return doc, nil
		}
		lastErr = err
		if _, ok := errors.IsBizError(err); ok {
			continue
		}
		return nil, lastErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, domain.ErrParserUnsupported
}
