DROP INDEX IF EXISTS knowledge.idx_wiki_pages_title_trgm;

-- pg_trgm extension is intentionally NOT dropped — it may be used by
-- other features or databases in the same cluster.
