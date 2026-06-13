-- Enable pg_trgm extension for trigram similarity matching.
-- Used by the wiki dedup pre-filter to find candidate existing pages
-- by title similarity (backed by idx_wiki_pages_title_trgm below).
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- GIN index on lower(title) for fast trigram similarity lookups.
-- Supports the % operator and similarity() function used by
-- FindSimilarPages in the wiki ingest dedup pipeline.
CREATE INDEX IF NOT EXISTS idx_wiki_pages_title_trgm
    ON knowledge.wiki_pages USING GIN (lower(title) gin_trgm_ops);
