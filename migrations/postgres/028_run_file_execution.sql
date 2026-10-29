-- Fixed-script Run execution identity and idempotent formal file registration.
-- Output grants retain immutable source authority; direct file reads revalidate
-- those dependencies even after terminal completion or a Bot backfill crash.
DO $run_file_execution$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 28);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '028_run_file_execution') THEN
        RETURN;
    END IF;

    CREATE TABLE IF NOT EXISTS bot.run_executions (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL REFERENCES bot.runs(id),
        generation BIGINT NOT NULL CHECK (generation > 0),
        input_version BIGINT NOT NULL CHECK (input_version > 0),
        skill_version_id UUID NOT NULL,
        skill_digest TEXT NOT NULL,
        input_file_id UUID NOT NULL,
        input_sha256 TEXT NOT NULL,
        input_bytes BIGINT NOT NULL CHECK (input_bytes >= 0),
        image_id TEXT NOT NULL,
        worker_sha256 TEXT NOT NULL,
        entry_point TEXT NOT NULL,
        review_digest TEXT NOT NULL,
        execution_id UUID,
        container_id TEXT,
        phase TEXT NOT NULL CHECK (phase IN ('intent','created','running','stopping','cleaned','unknown')),
        outcome TEXT NOT NULL CHECK (outcome IN ('unknown','succeeded','failed','timeout','output_limit','stopped')),
        failure_class TEXT NOT NULL DEFAULT '',
        output_sha256 TEXT NOT NULL DEFAULT '',
        output_bytes BIGINT NOT NULL DEFAULT 0 CHECK (output_bytes >= 0),
        result_file_id UUID,
        result_grant_id UUID,
        evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE (run_id)
    );
    CREATE INDEX IF NOT EXISTS idx_run_executions_open
        ON bot.run_executions(updated_at) WHERE phase <> 'cleaned';

    CREATE TABLE IF NOT EXISTS file.output_registrations (
        run_id UUID NOT NULL,
        generation BIGINT NOT NULL,
        bot_id UUID NOT NULL,
        conv_id UUID NOT NULL,
        binding_id UUID NOT NULL,
        file_id UUID NOT NULL UNIQUE REFERENCES file.files(id),
        source_access JSONB NOT NULL,
        source_tools JSONB,
        expires_at TIMESTAMPTZ NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        PRIMARY KEY (run_id, generation)
    );

    INSERT INTO public.schema_migrations(version)
        VALUES ('028_run_file_execution') ON CONFLICT DO NOTHING;
END;
$run_file_execution$;
