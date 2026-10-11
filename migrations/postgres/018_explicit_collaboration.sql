-- Nonhuman inputs have provenance, not fabricated human requester identities.
DO $explicit_collaboration$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '018_explicit_collaboration') THEN RETURN; END IF;
    ALTER TABLE bot.runs ALTER COLUMN requester_id DROP NOT NULL;
    ALTER TABLE bot.run_inputs ALTER COLUMN requester_id DROP NOT NULL;
    ALTER TABLE bot.interactions ALTER COLUMN requester_id DROP NOT NULL;
    ALTER TABLE bot.run_actions ALTER COLUMN requester_id DROP NOT NULL;
    ALTER TABLE bot.runs ADD COLUMN origin JSONB, ADD COLUMN activity JSONB,
        ADD COLUMN delegation_parent_run_id UUID REFERENCES bot.runs(id),
        ADD COLUMN delegation_root_run_id UUID REFERENCES bot.runs(id),
        ADD COLUMN delegation_depth INTEGER NOT NULL DEFAULT 0,
        ADD COLUMN delegation_input_bytes BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN delegation_output_tokens BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN delegation_source_generation BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN delegation_source_input_version BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN child_run_ids JSONB NOT NULL DEFAULT '[]';
    CREATE INDEX idx_runs_delegation_root ON bot.runs(delegation_root_run_id);
    CREATE UNIQUE INDEX idx_runs_nonhuman_input ON bot.runs(bot_id,binding_id,input_key) WHERE requester_id IS NULL;
    ALTER TABLE bot.runs ADD CONSTRAINT runs_origin_requester CHECK ((requester_id IS NOT NULL AND origin IS NULL) OR (requester_id IS NULL AND origin->>'kind' IN ('rule','delegation')));
    INSERT INTO public.schema_migrations(version) VALUES ('018_explicit_collaboration') ON CONFLICT DO NOTHING;
END;
$explicit_collaboration$;
