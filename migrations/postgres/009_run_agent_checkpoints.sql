-- ADK execution checkpoints are executor-private control-flow state, not Run trajectory blocks.
DO $run_agent_checkpoint$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '009_run_agent_checkpoints') THEN
        RETURN;
    END IF;

    CREATE TABLE bot.run_agent_checkpoints (
        run_id UUID PRIMARY KEY REFERENCES bot.runs(id) ON DELETE CASCADE,
        checkpoint BYTEA NOT NULL,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    INSERT INTO public.schema_migrations(version) VALUES ('009_run_agent_checkpoints') ON CONFLICT DO NOTHING;
END;
$run_agent_checkpoint$;
