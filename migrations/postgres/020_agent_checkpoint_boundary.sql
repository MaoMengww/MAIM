-- Framework checkpoints are usable only at the matching durable platform boundary.
DO $checkpoint_boundary$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '020_agent_checkpoint_boundary') THEN
        RETURN;
    END IF;

    ALTER TABLE bot.run_agent_checkpoints
        ADD COLUMN execution_generation BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN branch_version BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN source_revision BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN trajectory_version BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN input_upper_bound BIGINT NOT NULL DEFAULT 0,
        ADD COLUMN assembly_version TEXT NOT NULL DEFAULT '';
    -- Old opaque framework state has no verifiable platform boundary. The
    -- durable trajectory and immutable calls, not this state, recover the work.
    DELETE FROM bot.run_agent_checkpoints WHERE assembly_version = '';
    INSERT INTO public.schema_migrations(version) VALUES ('020_agent_checkpoint_boundary') ON CONFLICT DO NOTHING;
END;
$checkpoint_boundary$;
