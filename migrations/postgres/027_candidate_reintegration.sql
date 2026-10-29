-- Candidate reintegration creates immutable descendants while preserving one
-- root candidate per review and allowing every descendant to retain its review.
DO $candidate_reintegration$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 27);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '027_candidate_reintegration') THEN
        RETURN;
    END IF;

    ALTER TABLE bot.skill_candidates
        ADD COLUMN parent_candidate_id UUID REFERENCES bot.skill_candidates(id);

    DROP INDEX IF EXISTS bot.idx_skill_review_candidate;
    CREATE UNIQUE INDEX idx_skill_review_candidate_root
        ON bot.skill_candidates(source_review_id)
        WHERE source_review_id IS NOT NULL AND parent_candidate_id IS NULL;
    CREATE INDEX idx_skill_candidates_source_review
        ON bot.skill_candidates(source_review_id)
        WHERE source_review_id IS NOT NULL;

    INSERT INTO public.schema_migrations(version)
        VALUES ('027_candidate_reintegration') ON CONFLICT DO NOTHING;
END;
$candidate_reintegration$;
