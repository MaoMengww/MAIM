package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/maomeng/aim/app/knowledge-base/internal/domain"
)

const summaryTemplate = `## Role
You are a professional knowledge base analysis assistant. Your task is to generate knowledge point summaries based on document content.

## Constraints
1. use chinese to respond, except for proper nouns and technical terms
2. Elaborate based on the document content, ensuring all important information is covered
3. Organize in the following structure:
   - Core topic 
   - Key knowledge points
   - Relationships between knowledge points (if any)
4. Do not output any irrelevant content
5. If the document content is empty, output "No content"

## Document Content
%s

## Output Requirements
Plain text, UTF-8.`

const combinedExtractionTemplate = `## Role
You are a knowledge extraction expert. Extract both entities and concepts from the document.

## Constraints
1. Output only a valid JSON object containing "entities" and "concepts" arrays
2. Do not output any other text, explanations, or markdown
3. JSON must be valid and directly parseable by json.Unmarshal
4. Entities: specific named things such as people, systems, protocols, organizations, products
5. Concepts: abstract ideas, theories, mechanisms, properties, methodologies
6. Important: A topic can only appear in either entities or concepts, not both
7. If unsure whether it is an entity or concept, prioritize as entity
8. Each object contains:
   - "name": string, standard name
   - "description": string, less than 300 characters
   - "aliases": array of strings, alternative names/abbreviations
9. Maximum 20 entities and 15 concepts (prioritize entity count)
10. Write names and descriptions in Chinese. Do NOT translate proper nouns or technical terms.

## Document Content
%s

## Output
{"entities": [{"name": "entity name", "description": "entity description", "aliases": ["alias"]}], "concepts": [{"name": "concept name", "description": "concept explanation", "aliases": ["synonym"]}]}`

const synthesisTemplate = `## Role
You are a knowledge base analysis expert. Analyze the following document content and generate cross-document synthesis reviews.

## Constraints
1. use chinese to respond, except for proper nouns and technical terms
2. Output only a valid JSON array, do not output any other text, explanations, or markdown
3. JSON must be valid and directly parseable by json.Unmarshal
4. Based on document richness, generate 0-5 reviews. Return empty array [] when content is insufficient or unsuitable for synthesis
5. Each review is an object with the following fields:
   - "slug": string, URL-friendly identifier (e.g., "raft-consensus-overview"), using lowercase letters, digits and hyphens
   - "title": string, meaningful title (e.g., "Raft Consensus Algorithm Overview")
   - "content": string, complete review content, structured as: topic overview → point-by-point discussion → relationships → conclusion; elaborate based on document content covering all important information
6. Each review should focus on one independent topic, do not generate multiple reviews for one topic
7. If multiple documents present different perspectives on the same topic, synthesize and compare
8. Do not simply list items, integrate organically

## Document Content
%s

## Output
[{"slug": "topic-identifier", "title": "meaningful title", "content": "complete review content"}]`

const comparisonTemplate = `## Role
You are a knowledge comparison analysis expert. Find comparable entity or concept pairs in the documents and generate comparison analysis.

## Constraints
1. use chinese to respond, except for proper nouns and technical terms
2. Output only a valid JSON array, do not output any other text, explanations, or markdown
3. JSON must be valid and directly parseable by json.Unmarshal
4. Based on document content, generate 0-5 comparisons. Return empty array [] when no comparable objects exist
5. Only compare similar types (entity vs entity, concept vs concept)
6. Each comparison is an object with the following fields:
   - "slug": string, URL-friendly identifier (e.g., "raft-vs-paxos"), using lowercase letters, digits and hyphens
   - "title": string, meaningful title (e.g., "Raft vs Paxos Consensus Algorithm Comparison")
   - "content": string, complete comparison content, structured as:
     # {TitleA} vs {TitleB}
     ### Common Ground
     ### Differences
     | Dimension | {A} | {B} |
     |-----------|-----|-----|
     ### Use Cases
     ### Summary

## Document Content
%s

## Output
[{"slug": "comparison-identifier", "title": "meaningful title", "content": "complete comparison content"}]`

type wikiSynthesisItem struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type wikiComparisonItem struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type combinedExtraction struct {
	Entities []wikiEntity  `json:"entities"`
	Concepts []wikiConcept `json:"concepts"`
}

func buildSummaryPrompt(kbName, content string) string {
	return fmt.Sprintf(summaryTemplate, content)
}

func buildExtractionPrompt(content string) string {
	return fmt.Sprintf(combinedExtractionTemplate, content)
}

func buildSynthesisPrompt(content string) string {
	return fmt.Sprintf(synthesisTemplate, content)
}

func buildComparisonPrompt(content string) string {
	return fmt.Sprintf(comparisonTemplate, content)
}

const logSummaryTemplate = `Generate a concise change summary (30-50 words) based on the following change record.

Operation type: %s
Change details: %s

Output only the summary content, no prefix. Write the summary in Chinese.`

// generateLogSummary calls LLM to generate a change summary, falls back to raw detail on failure
func generateLogSummary(ctx context.Context, llm LLMGateway, modelID int64, ownerID int64, action, detail string) string {
	if llm == nil || modelID == 0 {
		return detail
	}
	resp, err := llm.Chat(ctx, &LLMChatRequest{
		ModelId: modelID,
		OwnerID: ownerID,
		Messages: []*LLMMessage{
			{Role: "system", Content: "You are a concise change log assistant."},
			{Role: "user", Content: fmt.Sprintf(logSummaryTemplate, action, detail)},
		},
		Params: map[string]any{"temperature": 0.3},
	})
	if err != nil || resp == nil || resp.Content == "" {
		return detail
	}
	return resp.Content
}

const candidateExtractionTemplate = `You are a knowledge extraction system. Analyze the following document and extract all significant entities AND key concepts.

<document>
<content>
{{.Content}}
</content>
</document>

<previous_slugs>
{{.PreviousSlugs}}
</previous_slugs>

<instructions>
Return a JSON object with two arrays: "entities" and "concepts".
**IMPORTANT: Write ALL names, descriptions, and details in Chinese, except for proper nouns and technical terms**.

If the <content> block above is empty, contains only image references with no extracted text, or otherwise carries no substantive information, return {"entities": [], "concepts": []}. Do NOT invent entities or concepts from any other source.

### Slug Continuity Rules
If previous slugs are provided above, you MUST follow these rules:
- If an entity or concept from the previous extraction still exists in the current document, **reuse its exact slug** from the previous list. Do NOT generate a new slug for the same thing.
- If an entity or concept no longer appears in the document, do **NOT include it** in the output.
- Only generate new slugs for entities/concepts that are genuinely new (not present in the previous list).
- This ensures slug stability across document updates.

### Entities (people, organizations, products, places, technologies, events, etc.)
Each entity should have:
- "name": The entity name in Chinese (human-readable)
- "slug": URL-friendly slug, format "entity/<lowercase-hyphenated-name>" (use romanized/pinyin form for non-Latin names). **Reuse previous slug if the entity was extracted before.**
- "aliases": An array of strings representing names that refer to THE EXACT SAME entity. Only include: official abbreviations (e.g. "IBM" for "International Business Machines"), full/short name variants (e.g. "腾讯" for "腾讯控股有限公司"), translations (e.g. "Apple" for "苹果公司"), and well-known alternate names (e.g. "Alphabet" for "Google母公司"). Do NOT include parent categories, related products, generic terms, or broader concepts. Provide [] if none.
- "description": **Index listing summary** — one sentence, 15-40 words, in Chinese. Describes WHAT this entity IS and its role in the document. Must be self-contained (understandable without reading the full page). This will be displayed in the wiki index.
- "details": <300 characters in Chinese. Key facts about this entity from the document. **Image rule**: If the document contains relevant <image> elements in an <images> tag, include them in the details using Markdown syntax: ![caption](url).

Only include entities that are substantively discussed. Do NOT include generic terms.

### Concepts (topics, themes, methodologies, theories, etc.)
Each concept should have:
- "name": The concept name in Chinese (human-readable)
- "slug": URL-friendly slug, format "concept/<lowercase-hyphenated-name>" (use romanized/pinyin form for non-Latin names). **Reuse previous slug if the concept was extracted before.**
- "aliases": An array of strings representing names that refer to THE EXACT SAME concept. Only include: official abbreviations (e.g. "RAG" for "Retrieval-Augmented Generation"), full/short name variants, and well-known synonyms used interchangeably in the field. Do NOT include sub-topics, related techniques, broader categories, or implementation details. Provide [] if none.
- "description": **Index listing summary** — one sentence, 15-40 words, in Chinese. Defines WHAT this concept IS. Must be self-contained (understandable without reading the full page). This will be displayed in the wiki index.
- "details": <300 characters in Chinese. Key facts about this concept from the document. **Image rule**: If the document contains relevant <image> elements in an <images> tag, include them in the details using Markdown syntax: ![caption](url).

Only include concepts that are substantively discussed. Skip trivial or overly generic concepts.

### Deduplication Rules
- If something is a specific named thing (person, company, product, place), put it ONLY in "entities".
- If something is an abstract idea, methodology, or theory, put it ONLY in "concepts".
- Never duplicate items across the two arrays.

### JSON Formatting Rules
- **CRITICAL**: Do NOT use literal newline characters inside JSON string values. If you need a newline in a string, you MUST use the escaped sequence \n.
</instructions>

Output ONLY valid JSON. Example:
{
  "entities": [
    {
      "name": "Acme Corp",
      "slug": "entity/acme-corp",
      "aliases": ["Acme", "Acme Corporation"],
      "description": "A technology company specializing in AI solutions.",
      "details": "Acme Corp was founded in 2020 and has grown to 500 employees. They focus on enterprise AI products and recently launched their flagship RAG platform."
    }
  ],
  "concepts": [
    {
      "name": "Retrieval-Augmented Generation",
      "slug": "concept/retrieval-augmented-generation",
      "aliases": ["RAG"],
      "description": "A technique that combines information retrieval with language model generation.",
      "details": "RAG works by first retrieving relevant documents from a knowledge base using vector similarity search, then feeding those documents as context to an LLM for answer generation."
    }
  ]
}`

type candidateExtractionInput struct {
	Content       string
	PreviousSlugs string
}

func buildCandidateExtractionPrompt(content, previousSlugs string) string {
	var buf bytes.Buffer
	tmpl, err := template.New("candidateExtraction").Parse(candidateExtractionTemplate)
	if err != nil {
		return `{"entities":[],"concepts":[]}`
	}
	if err := tmpl.Execute(&buf, candidateExtractionInput{Content: content, PreviousSlugs: previousSlugs}); err != nil {
		return `{"entities":[],"concepts":[]}`
	}
	return buf.String()
}

const reduceMergeTemplateStr = `You are a wiki editor tasked with updating an existing wiki page with new extracted information.

<page_metadata>
<slug>{{.PageSlug}}</slug>
<title>{{.PageTitle}}</title>
<type>{{.PageType}}</type>
</page_metadata>

<existing_summary>
{{.ExistingSummary}}
</existing_summary>

<existing_page_content>
{{.ExistingContent}}
</existing_page_content>

<new_extracted_information>
{{.NewExtractions}}
</new_extracted_information>

Instructions:
1. FIRST line: SUMMARY: {one sentence}
2. MERGE the new extracted information into the existing page content
3. Preserve valid existing information
4. Write in Chinese. Do NOT translate proper nouns or technical terms.

Output SUMMARY line first, then updated Markdown.`

type reduceMergeTemplateData struct {
	PageSlug        string
	PageTitle       string
	PageType        string
	ExistingSummary string
	ExistingContent string
	NewExtractions  string
}

func buildReduceMergePrompt(page *domain.WikiPage, newInputs []mergeInput) string {
	var sb strings.Builder
	for i, in := range newInputs {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		sb.WriteString(fmt.Sprintf("Name: %s\n", in.Name))
		if len(in.Aliases) > 0 {
			sb.WriteString(fmt.Sprintf("Aliases: %s\n", strings.Join(in.Aliases, ", ")))
		}
		sb.WriteString(fmt.Sprintf("Description: %s\n", in.Description))
		sb.WriteString(fmt.Sprintf("Details: %s\n", in.Details))
	}
	data := reduceMergeTemplateData{
		PageSlug:        page.Slug,
		PageTitle:       page.Title,
		PageType:        string(page.PageType),
		ExistingSummary: page.Summary,
		ExistingContent: page.Content,
		NewExtractions:  sb.String(),
	}
	var buf bytes.Buffer
	tmpl, err := template.New("reduceMerge").Parse(reduceMergeTemplateStr)
	if err != nil {
		return ""
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}

// ================================================================
// Wiki dedup prompt — LLM decides which new items merge into
// existing pages. Modeled on WeKnora's WikiDeduplicationPrompt.
// ================================================================

const wikiDedupTemplate = `You are a strict deduplication system. Given a list of newly extracted items and a list of existing wiki pages, determine which new items refer to the **exact same** real-world entity or concept as an existing page.

<new_items>
{{.NewItems}}
</new_items>

<existing_pages>
{{.ExistingPages}}
</existing_pages>

<instructions>
### Merge criteria — ALL must be true:
1. The new item and the existing page refer to the **same real-world thing** (same algorithm, same protocol, same specific concept).
2. The match is a **name variation**: abbreviation ↔ full name, alternative spelling/translation, or minor surface difference (e.g. spaces vs no spaces, "算法" vs "共识算法" suffixed).
3. The types are compatible: entities merge with entities, concepts merge with concepts. **Never merge an entity into a concept or vice versa.**

### Examples of CORRECT merges:
- "Paxos算法" → "Paxos 算法" (same algorithm, space difference)
- "Raft共识算法" → "Raft 算法" (same algorithm, suffix difference)
- "RAG" → "Retrieval-Augmented Generation" (same concept, acronym)
- "苹果公司" → "Apple Inc." (same entity, translation)

### Examples of INCORRECT merges — do NOT merge:
- "Paxos" → "Raft" (different algorithms in the same category)
- "Multi-Paxos" → "Paxos" (variant vs base — only merge if they describe the SAME thing)
- "GPT-4" → "GPT-3.5" (different specific versions)
- "Machine Learning" → "Neural Networks" (subset vs superset)
- "居民身份证" → "工作居住证" (different documents sharing a category)

### Key principle: **related ≠ same**. Two items sharing domain keywords, belonging to the same category, or appearing in the same document is NOT a reason to merge. When in doubt, do NOT merge. It is far better to have separate pages than to wrongly merge different things.

Return a JSON object with a "merges" map. The key is the NEW item's slug, the value is the EXISTING page's slug that it should merge into.

If no items match any existing pages, return: {"merges": {}}

### JSON Formatting Rules
- **CRITICAL**: Do NOT use literal newline characters inside JSON string values. If you need a newline in a string, you MUST use the escaped sequence \n.
</instructions>

Output ONLY valid JSON. Example:
{"merges": {"entity/paxos-algorithm": "entity/paxos", "concept/rag": "concept/retrieval-augmented-generation"}}`

type wikiDedupData struct {
	NewItems      string
	ExistingPages string
}

func buildDedupPrompt(newItems, existingPages string) string {
	var buf bytes.Buffer
	tmpl, err := template.New("wikiDedup").Parse(wikiDedupTemplate)
	if err != nil {
		return `{"merges":{}}`
	}
	if err := tmpl.Execute(&buf, wikiDedupData{NewItems: newItems, ExistingPages: existingPages}); err != nil {
		return `{"merges":{}}`
	}
	return buf.String()
}

// xmlEscape escapes characters that break XML text content.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// writeDedupItemXML renders a single entity/concept as an XML block for
// the dedup prompt. Structured form helps the LLM reliably tell name,
// aliases, and type apart.
func writeDedupItemXML(buf *bytes.Buffer, slug, name, itemType string, aliases []string) {
	fmt.Fprintf(buf, "  <item slug=%q type=%q>\n", slug, itemType)
	fmt.Fprintf(buf, "    <name>%s</name>\n", xmlEscape(name))
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		fmt.Fprintf(buf, "    <alias>%s</alias>\n", xmlEscape(alias))
	}
	buf.WriteString("  </item>\n")
}
