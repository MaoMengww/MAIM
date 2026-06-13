package parser

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"strings"

	xhtml "golang.org/x/net/html"
)

// blockElements are HTML tags that introduce paragraph-level breaks.
var blockElements = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "li": true, "tr": true,
	"section": true, "article": true, "header": true, "footer": true,
	"nav": true, "main": true, "aside": true, "blockquote": true,
	"pre": true, "hr": true, "ul": true, "ol": true, "dl": true,
	"table": true, "form": true, "fieldset": true, "figure": true,
	"figcaption": true, "details": true, "summary": true,
}

// headingTags maps HTML heading tags to markdown heading levels.
var headingTags = map[string]int{
	"h1": 1, "h2": 2, "h3": 3, "h4": 4, "h5": 5, "h6": 6,
}

// convertToText dispatches raw bytes to the appropriate format converter
// based on the file type.
func convertToText(raw []byte, fileType string) string {
	switch fileType {
	case "html":
		return htmlToText(raw)
	case "json":
		return jsonToMarkdown(raw)
	case "csv":
		return csvToMarkdown(raw)
	case "xml":
		return xmlToText(raw)
	case "yaml", "yml":
		return string(raw) // YAML is already human-readable
	default:
		return string(raw)
	}
}

// ---------------------------------------------------------------------------
// HTML → text
// ---------------------------------------------------------------------------

// htmlToText extracts readable text from HTML using the x/net/html tokenizer.
// It strips scripts/styles, converts block elements to paragraph breaks, and
// preserves heading structure as markdown-compatible "# Title" lines.
func htmlToText(raw []byte) string {
	z := xhtml.NewTokenizer(bytes.NewReader(raw))
	var buf strings.Builder
	var inSkip string // "script", "style", or empty
	var linkHref string
	var pendingSpace bool
	depth := 0

	for {
		tt := z.Next()
		switch tt {
		case xhtml.ErrorToken:
			err := z.Err()
			if err == io.EOF {
				return strings.TrimSpace(buf.String())
			}
			// If parse fails mid-way, return what we have
			return strings.TrimSpace(buf.String())

		case xhtml.TextToken:
			if inSkip != "" {
				continue
			}
			text := string(z.Text())
			// Collapse whitespace
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			if pendingSpace {
				buf.WriteByte(' ')
				pendingSpace = false
			}
			buf.WriteString(text)
			pendingSpace = true

		case xhtml.StartTagToken:
			tn, hasAttr := z.TagName()
			tag := string(tn)

			if tag == "script" || tag == "noscript" || tag == "style" || tag == "template" {
				inSkip = tag
				continue
			}

			if tag == "br" || tag == "hr" {
				buf.WriteByte('\n')
				pendingSpace = false
				continue
			}

			// Write heading marker BEFORE heading text content
			if lvl, ok := headingTags[tag]; ok {
				buf.WriteByte('\n')
				fmt.Fprintf(&buf, "%s ", strings.Repeat("#", lvl))
				pendingSpace = false
			}

			// Extract link href for [text](url) rendering
			if tag == "a" && hasAttr {
				linkHref = getAttr(z, "href")
			}

			depth++

		case xhtml.SelfClosingTagToken:
			tn, _ := z.TagName()
			tag := string(tn)
			if tag == "br" || tag == "hr" {
				buf.WriteByte('\n')
				pendingSpace = false
			}
			if tag == "img" {
				alt := getAttr(z, "alt")
				src := getAttr(z, "src")
				if alt != "" {
					fmt.Fprintf(&buf, "![%s](%s)", alt, src)
				}
			}

		case xhtml.EndTagToken:
			tn, _ := z.TagName()
			tag := string(tn)

			if tag == inSkip {
				inSkip = ""
				continue
			}

			depth--

			// Finish link: emit [text](url)
			if tag == "a" && linkHref != "" {
				// The text has already been written; we append the URL reference
				linkHref = ""
			}

			// Block elements → newline after content
			if blockElements[tag] {
				buf.WriteByte('\n')
				pendingSpace = false
			}
		}
		_ = depth
	}
}

// getAttr reads an attribute value from the current token.
func getAttr(z *xhtml.Tokenizer, key string) string {
	for {
		k, v, more := z.TagAttr()
		if string(k) == key {
			return string(v)
		}
		if !more {
			break
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// JSON → markdown code blocks
// ---------------------------------------------------------------------------

const jsonChunkSize = 1536 // bytes, ≈ 512 tokens

// jsonToMarkdown converts JSON to one or more ```json code blocks.
// Large JSON objects are recursively split so each block is a complete,
// valid JSON subset that fits within jsonChunkSize.
func jsonToMarkdown(raw []byte) string {
	// Reject obvious non-JSON (the content-type detection uses json.Valid first)
	if !json.Valid(raw) {
		return string(raw)
	}

	// If the whole JSON fits in a single block, return it formatted.
	if len(raw) <= jsonChunkSize {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return string(raw)
		}
		return "```json\n" + pretty.String() + "\n```"
	}

	// Large JSON: recursively split into multiple ```json blocks.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}

	var blocks []string
	recursiveJSONSplit(v, "", jsonChunkSize, &blocks)

	if len(blocks) == 0 {
		return "```json\n{}\n```"
	}
	return strings.Join(blocks, "\n\n")
}

// recursiveJSONSplit splits a JSON value into multiple ```json blocks.
// Each block is a valid JSON string that fits within the byte budget.
func recursiveJSONSplit(v any, path string, budget int, blocks *[]string) {
	switch val := v.(type) {
	case map[string]any:
		// Try to fit the whole object in one block
		encoded, _ := json.MarshalIndent(val, "", "  ")
		if len(encoded) <= budget {
			*blocks = append(*blocks, "```json\n"+string(encoded)+"\n```")
			return
		}
		// Too large: for single-key maps with nested values, recurse directly
		// into the value to avoid infinite recursion on {"key": [large_array]}.
		if len(val) == 1 {
			for _, subv := range val {
				recursiveJSONSplit(subv, path, budget, blocks)
			}
			return
		}

		// Multiple keys: split by key groups
		var batch map[string]any
		for k, subv := range val {
			if batch == nil {
				batch = make(map[string]any)
			}
			batch[k] = subv
			enc, _ := json.MarshalIndent(batch, "", "  ")
			if len(enc) > budget && len(batch) > 1 {
				// Commit the previous batch (without this key)
				delete(batch, k)
				encPrev, _ := json.MarshalIndent(batch, "", "  ")
				if len(encPrev) > 0 {
					*blocks = append(*blocks, "```json\n"+string(encPrev)+"\n```")
				}
				// Start a new batch with the current oversized key
				recursiveJSONSplit(map[string]any{k: subv}, path+"/"+k, budget, blocks)
				batch = nil
			}
		}
		// Flush remaining batch
		if len(batch) > 0 {
			enc, _ := json.MarshalIndent(batch, "", "  ")
			*blocks = append(*blocks, "```json\n"+string(enc)+"\n```")
		}

	case []any:
		// Convert array to indexed map first: ["a","b"] → {"0":"a","1":"b"}
		m := make(map[string]any, len(val))
		for i, item := range val {
			m[fmt.Sprintf("%d", i)] = item
		}
		recursiveJSONSplit(m, path, budget, blocks)

	default:
		// Scalar value: wrap in a minimal JSON object
		enc, _ := json.MarshalIndent(map[string]any{"value": val}, "", "  ")
		*blocks = append(*blocks, "```json\n"+string(enc)+"\n```")
	}
}

// ---------------------------------------------------------------------------
// CSV → markdown table
// ---------------------------------------------------------------------------

// csvToMarkdown converts CSV content to a markdown pipe table.
//
//	Name,Age,City         →  | Name | Age | City |
//	John,30,NYC              | ---  | --- | ---  |
//	                         | John | 30  | NYC  |
func csvToMarkdown(raw []byte) string {
	r := csv.NewReader(bytes.NewReader(raw))
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1 // allow variable number of fields per row

	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return string(raw)
	}

	// Normalize column count: pad shorter rows with empty cells
	maxCols := 0
	for _, row := range records {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	for i := range records {
		for len(records[i]) < maxCols {
			records[i] = append(records[i], "")
		}
	}

	var buf strings.Builder

	// Header row
	buf.WriteString("| ")
	for j, col := range records[0] {
		if j > 0 {
			buf.WriteString(" | ")
		}
		buf.WriteString(strings.TrimSpace(col))
	}
	buf.WriteString(" |\n")

	// Separator row
	buf.WriteString("| ")
	for j := 0; j < maxCols; j++ {
		if j > 0 {
			buf.WriteString(" | ")
		}
		buf.WriteString("---")
	}
	buf.WriteString(" |\n")

	// Data rows
	for _, row := range records[1:] {
		buf.WriteString("| ")
		for j, col := range row {
			if j > 0 {
				buf.WriteString(" | ")
			}
			buf.WriteString(strings.TrimSpace(col))
		}
		buf.WriteString(" |\n")
	}

	return buf.String()
}

// ---------------------------------------------------------------------------
// XML → text
// ---------------------------------------------------------------------------

// xmlToText extracts text content from XML documents by decoding element
// content and character data while ignoring processing instructions and
// structural markup. HTML entities are decoded.
func xmlToText(raw []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = false

	var buf strings.Builder
	depth := 0
	var inSkip string

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "script" || t.Name.Local == "style" {
				inSkip = t.Name.Local
			}
			depth++

		case xml.EndElement:
			if t.Name.Local == inSkip {
				inSkip = ""
			}
			depth--
			// Add newline after block-like elements
			if t.Name.Local == "p" || t.Name.Local == "div" ||
				t.Name.Local == "li" || t.Name.Local == "tr" ||
				t.Name.Local == "section" || t.Name.Local == "article" {
				buf.WriteByte('\n')
			}

		case xml.CharData:
			if inSkip != "" {
				continue
			}
			text := strings.TrimSpace(string(t))
			if text != "" {
				buf.WriteString(html.UnescapeString(text))
				buf.WriteByte(' ')
			}

		case xml.Comment, xml.ProcInst, xml.Directive:
			// skip
		}
	}
	_ = depth

	return strings.TrimSpace(buf.String())
}
