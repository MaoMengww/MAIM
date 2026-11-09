-- Registered artifacts are independent resources, not live source-grant chains.
DO $output_snapshot_permissions$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 31);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '031_output_snapshot_permissions') THEN
        RETURN;
    END IF;

    -- Earlier registrations capped the artifact's original grant at its source
    -- expiry. Ninety days is the existing artifact-owned default lifetime.
    -- Restore only that original grant, preserving explicit revocations and
    -- independently issued grants or their manually chosen lifetimes.
    UPDATE file.resource_grants grant_row
    SET expires_at = grant_row.created_at + INTERVAL '90 days'
    FROM file.output_registrations registration
    WHERE grant_row.resource_id = registration.file_id
      AND grant_row.owner_id = registration.bot_id
      AND grant_row.bot_id = registration.bot_id
      AND grant_row.binding_id = registration.binding_id
      AND grant_row.created_at = registration.created_at
      AND grant_row.expires_at = registration.expires_at
      AND grant_row.revoked_at IS NULL
      AND grant_row.expires_at < grant_row.created_at + INTERVAL '90 days';

    ALTER TABLE file.output_registrations
        DROP COLUMN IF EXISTS source_access,
        DROP COLUMN IF EXISTS source_tools,
        DROP COLUMN IF EXISTS expires_at;

    INSERT INTO public.schema_migrations(version)
        VALUES ('031_output_snapshot_permissions') ON CONFLICT DO NOTHING;
END;
$output_snapshot_permissions$;
