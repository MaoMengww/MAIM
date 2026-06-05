package parser

import (
	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

// SupportedFileTypes returns the file types each engine supports.
var engineFileTypes = map[string][]string{
	"builtin":          {"txt", "md", "html", "json", "xml", "csv", "yaml", "yml"},
	"mineru_precision": {"pdf", "doc", "docx", "ppt", "pptx", "xls", "xlsx", "png", "jpg", "jpeg", "bmp", "tiff"},
	"mineru":           {"pdf", "doc", "docx", "ppt", "pptx", "xls", "xlsx", "png", "jpg", "jpeg", "bmp", "tiff"},
}

// SetupParser returns a Parser for the given config.
// It tries each engine in order (cfg.Engines) and returns a chain of all parsers
// whose SupportedFileTypes include the given fileType.
// If no engine supports the file type, it returns nil (caller should treat as
// ErrParserUnsupported).
func SetupParser(cfg domain.ParsingConfig, fileType string) domain.Parser {
	var parsers []domain.Parser
	for _, engine := range cfg.Engines {
		if !supportsFileType(engine, fileType) {
			continue
		}
		switch engine {
		case "builtin":
			parsers = append(parsers, NewBuiltinParser())
		case "mineru_precision":
			if cfg.MinerUPrecision == nil || cfg.MinerUPrecision.APIURL == "" {
				continue
			}
			parsers = append(parsers, NewMinerUParser(cfg.MinerUPrecision.APIURL, cfg.MinerUPrecision.APIToken))
		case "mineru":
			if cfg.MinerUAgent == nil || cfg.MinerUAgent.APIKey == "" {
				continue
			}
			parsers = append(parsers, NewMinerUCloudParser(cfg.MinerUAgent.APIKey, cfg.MinerUAgent.APIURL))
		}
	}
	if len(parsers) == 0 {
		return nil
	}
	if len(parsers) == 1 {
		return parsers[0]
	}
	return NewParserChain(parsers...)
}

func supportsFileType(engine, fileType string) bool {
	types, ok := engineFileTypes[engine]
	if !ok {
		return false
	}
	for _, t := range types {
		if t == fileType {
			return true
		}
	}
	return false
}
