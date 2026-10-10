DO $working_compaction$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '013_working_compaction') THEN RETURN; END IF;
    IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '012_action_confirmation_sharing') THEN
        RAISE EXCEPTION '012_action_confirmation_sharing must be applied before 013_working_compaction';
    END IF;
    CREATE TABLE bot.run_compaction_checkpoints (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL REFERENCES bot.runs(id),
        interaction_id UUID NOT NULL REFERENCES bot.interactions(id),
        version BIGINT NOT NULL CHECK (version > 0),
        branch_version BIGINT NOT NULL,
        source_revision BIGINT NOT NULL,
        covered_from BIGINT NOT NULL CHECK (covered_from > 1),
        covered_through BIGINT NOT NULL CHECK (covered_through >= covered_from),
        input_upper_bound BIGINT NOT NULL,
        prior_checkpoint_id UUID REFERENCES bot.run_compaction_checkpoints(id),
        source_versions JSONB NOT NULL,
        instruction_version TEXT NOT NULL,
        tools_version TEXT NOT NULL,
        assembly_version TEXT NOT NULL,
        summary JSONB NOT NULL,
        pinned_messages JSONB NOT NULL,
        omissions JSONB NOT NULL,
        status TEXT NOT NULL CHECK (status IN ('valid','invalid')),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE (run_id, version)
    );
    CREATE INDEX run_compaction_valid_idx ON bot.run_compaction_checkpoints(run_id, version DESC) WHERE status = 'valid';
    CREATE TABLE bot.run_compaction_calls (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL REFERENCES bot.runs(id),
        interaction_id UUID NOT NULL REFERENCES bot.interactions(id),
        execution_generation BIGINT NOT NULL,
        branch_version BIGINT NOT NULL,
        source_revision BIGINT NOT NULL,
        covered_from BIGINT NOT NULL,
        covered_through BIGINT NOT NULL,
        input_upper_bound BIGINT NOT NULL,
        prior_checkpoint_id UUID REFERENCES bot.run_compaction_checkpoints(id),
        request JSONB,
        response JSONB,
        usage JSONB,
        first_token_latency_ms BIGINT,
        status TEXT NOT NULL CHECK (status IN ('requested','completed','rejected','failed','invalidated')),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        completed_at TIMESTAMPTZ
    );
    CREATE INDEX run_compaction_calls_run_idx ON bot.run_compaction_calls(run_id,created_at);
    INSERT INTO public.schema_migrations(version) VALUES ('013_working_compaction') ON CONFLICT DO NOTHING;
END;
$working_compaction$;
