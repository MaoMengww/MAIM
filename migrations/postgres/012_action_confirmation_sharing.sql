DO $action_confirmation_sharing$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '012_action_confirmation_sharing') THEN RETURN; END IF;
    IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '011_run_actions') THEN
        RAISE EXCEPTION '011_run_actions must be applied before 012_action_confirmation_sharing';
    END IF;
    ALTER TABLE bot.run_actions
        ADD COLUMN IF NOT EXISTS confirmation_shared_at TIMESTAMPTZ DEFAULT NULL,
        ADD COLUMN IF NOT EXISTS confirmation_owner_id UUID DEFAULT NULL;
    INSERT INTO public.schema_migrations(version) VALUES ('012_action_confirmation_sharing') ON CONFLICT DO NOTHING;
END;
$action_confirmation_sharing$;
