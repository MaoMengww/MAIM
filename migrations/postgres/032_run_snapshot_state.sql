-- Accepted context and results no longer carry reverse source-revocation state.
DO $run_snapshot_state$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 32);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '032_run_snapshot_state') THEN
        RETURN;
    END IF;

    ALTER TABLE bot.runs DROP COLUMN IF EXISTS result_invalidated;
    ALTER TABLE bot.run_actions DROP COLUMN IF EXISTS source_invalidated;
    ALTER TABLE bot.run_reviews DROP COLUMN IF EXISTS evidence_revoked_at;
    UPDATE bot.runs
       SET access_revoked = FALSE
     WHERE access_revoked = TRUE
       AND stop_reason IN ('source_authorization_revoked', 'resource_authorization_revoked', 'tool_authorization_revoked', 'skill_version_disabled', 'delegation_source_revoked', 'origin_authorization_revoked');

    INSERT INTO public.schema_migrations(version)
        VALUES ('032_run_snapshot_state') ON CONFLICT DO NOTHING;
END;
$run_snapshot_state$;
