-- Execution ownership remains a durable platform fact across process restarts.
DO $safe_runtime_recovery$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '019_safe_runtime_recovery') THEN RETURN; END IF;
    ALTER TABLE bot.runs ADD COLUMN executor_lease_until TIMESTAMPTZ;
    ALTER TABLE bot.runs DROP CONSTRAINT runs_executor_status_check;
    ALTER TABLE bot.runs ADD CONSTRAINT runs_executor_status_check CHECK(executor_status IN ('pending','accepted','completed','stopped','stop_rejected','lost'));
    -- Pre-migration external claims have no proof of live ownership. Expire
    -- them; reconciliation preserves waits and requires an explicit resume.
    UPDATE bot.runs SET executor_lease_until = NOW() - INTERVAL '1 second'
        WHERE executor_type = 'third_party' AND executor_status IN ('accepted','stop_rejected');
    ALTER TABLE bot.run_outbox ADD COLUMN execution_generation BIGINT NOT NULL DEFAULT 0;
    UPDATE bot.run_outbox o SET execution_generation = r.execution_generation
        FROM bot.runs r WHERE o.run_id = r.id AND o.kind = 'execute'
        AND o.completed_at IS NULL AND o.lease_until IS NOT NULL;
    CREATE INDEX idx_run_outbox_execution_lease ON bot.run_outbox(run_id,execution_generation,lease_until)
        WHERE kind = 'execute' AND completed_at IS NULL;
    INSERT INTO public.schema_migrations(version) VALUES ('019_safe_runtime_recovery') ON CONFLICT DO NOTHING;
END;
$safe_runtime_recovery$;
