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
1. 请使用中文(专有名词除外)
2. Elaborate based on the document content, ensuring all important information is covered
3. Organize in the following structure:
   - Core topic (1-2 sentences summary)
   - Key knowledge points (3-5 points, 1-2 sentences each)
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
   - "description": string, 1-2 sentences
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
1. 请使用中文(专有名词除外)
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
1. 请使用中文(专有名词除外)
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

Output only the summary content, no prefix.`

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

const candidateExtractionTemplate = `You are a lightweight candidate extraction system. Analyze the following document and list all significant entities and key concepts as a lightweight candidate set.

<document>
{{.Content}}
</document>

<previous_slugs>
{{.PreviousSlugs}}
</previous_slugs>

Return a JSON object with "entities" and "concepts" arrays.
Each item: name, description (definition, core characteristics, key details), slug (entity/... format), aliases (array), doc_ids (array of integers).

IMPORTANT: Write names and descriptions in Chinese. Do NOT translate proper nouns or technical terms.
If content is empty -> {"entities": [], "concepts": []}.

Slug Continuity: If an entity/concept from previous_slugs still exists, reuse its exact slug. Only create new slugs for genuinely new items.

Dedup: Named thing -> entity. Abstract idea -> concept. Never duplicate.

STRICT: Output raw JSON only. NO markdown, NO code fences, NO explanation. ONLY the JSON object.`

type candidateExtractionInput struct {
	Content       string
	PreviousSlugs string
	Language      string
}

func buildCandidateExtractionPrompt(content, previousSlugs, language string) string {
	var buf bytes.Buffer
	tmpl, err := template.New("candidateExtraction").Parse(candidateExtractionTemplate)
	if err != nil {
		return `{"entities":[],"concepts":[]}`
	}
	if err := tmpl.Execute(&buf, candidateExtractionInput{Content: content, PreviousSlugs: previousSlugs, Language: language}); err != nil {
		return `{"entities":[],"concepts":[]}`
	}
	return buf.String()
}

const reduceMergeTemplateStr = `You are a wiki editor tasked with updating an existing wiki page with new information, and/or removing facts from deleted documents.

STRICT RULES:
1. Do NOT invent information not in source documents.
2. Stay close to source wording. No rhetorical filler.

<page_metadata>
<slug>{{.PageSlug}}</slug>
<title>{{.PageTitle}}</title>
<type>{{.PageType}}</type>
</page_metadata>

<existing_page_content>
{{.ExistingContent}}
</existing_page_content>

{{if .HasAdditions}}
<new_information>
{{.NewContent}}
</new_information>
{{end}}

{{if .HasRetractions}}
<deleted_document_ids>{{.DeletedDocIDs}}</deleted_document_ids>
<remaining_source_documents>
{{.RemainingSourcesContent}}
</remaining_source_documents>
{{end}}

Instructions:
1. FIRST line: SUMMARY: {one sentence}
2. REMOVE facts only from deleted documents that are not present in remaining sources
3. ADD/MERGE new information -- be a compiler, not a writer
4. Preserve valid existing information
5. Write in {{.Language}}

Output SUMMARY line first, then updated Markdown.`

type reduceMergeTemplateData struct {
	PageSlug                string
	PageTitle               string
	PageType                string
	ExistingContent         string
	HasAdditions            bool
	NewContent              string
	HasRetractions          bool
	DeletedDocIDs           string
	RemainingSourcesContent string
	Language                string
}

func buildReduceMergePrompt(page *domain.WikiPage, additions []string, deletedDocIDs []int64, language string) string {
	var newContent string
	hasAdditions := len(additions) > 0
	if hasAdditions {
		newContent = strings.Join(additions, "\n")
	}
	hasRetractions := len(deletedDocIDs) > 0
	var deletedIDsStr string
	if hasRetractions {
		idStrs := make([]string, len(deletedDocIDs))
		for i, id := range deletedDocIDs {
			idStrs[i] = fmt.Sprintf("%d", id)
		}
		deletedIDsStr = strings.Join(idStrs, ",")
	}
	data := reduceMergeTemplateData{
		PageSlug:        page.Slug,
		PageTitle:       page.Title,
		PageType:        string(page.PageType),
		ExistingContent: page.Content,
		HasAdditions:    hasAdditions,
		NewContent:      newContent,
		HasRetractions:  hasRetractions,
		DeletedDocIDs:   deletedIDsStr,
		Language:        language,
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
