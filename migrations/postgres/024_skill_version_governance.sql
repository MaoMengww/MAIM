-- Scoped immutable-version adoption, rollback watermarks and emergency revocation.
DO $skill_version_governance$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '024_skill_version_governance') THEN
        RETURN;
    END IF;
    ALTER TABLE bot.skill_bindings ADD COLUMN revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
    ALTER TABLE bot.skill_bindings ADD COLUMN rollback_hold_version INTEGER NOT NULL DEFAULT 0;
    ALTER TABLE bot.skill_bindings ADD COLUMN blocked_version_id UUID REFERENCES bot.skill_versions(id);
    ALTER TABLE bot.skill_bindings ADD COLUMN blocked_reasons JSONB NOT NULL DEFAULT '[]'::jsonb;
    CREATE TABLE bot.skill_version_activities (
        id UUID PRIMARY KEY,
        bot_id UUID NOT NULL,
        skill_id UUID NOT NULL REFERENCES bot.skill_packages(id),
        scope_key TEXT NOT NULL,
        actor_id UUID NOT NULL,
        action TEXT NOT NULL CHECK (action IN ('publish','auto_upgrade','upgrade_blocked','confirm','rollback','disable')),
        version_id UUID NOT NULL REFERENCES bot.skill_versions(id),
        version INTEGER NOT NULL,
        digest TEXT NOT NULL,
        reason TEXT NOT NULL,
        binding_revision BIGINT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    CREATE INDEX skill_version_activity_scope ON bot.skill_version_activities(bot_id,skill_id,scope_key,created_at);
    INSERT INTO public.schema_migrations(version) VALUES ('024_skill_version_governance') ON CONFLICT DO NOTHING;
END;
$skill_version_governance$;
