DO $resource_authorization$
BEGIN
 PERFORM pg_advisory_xact_lock(4278605, 1);
 IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version='010_resource_authorization') THEN RETURN; END IF;
 IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version='009_run_agent_checkpoints') THEN RAISE EXCEPTION '009_run_agent_checkpoints required'; END IF;
 ALTER TABLE bot.runs ADD COLUMN visibility TEXT NOT NULL DEFAULT 'conversation' CHECK (visibility IN ('conversation','personal'));
 ALTER TABLE bot.runs ADD COLUMN personal_user_id UUID;
 ALTER TABLE bot.runs ADD COLUMN resource_grant_ids JSONB NOT NULL DEFAULT '[]';
 ALTER TABLE bot.interactions ADD COLUMN visibility TEXT NOT NULL DEFAULT 'conversation' CHECK (visibility IN ('conversation','personal'));
 ALTER TABLE bot.interactions ADD COLUMN personal_user_id UUID;
 CREATE TABLE file.resource_grants (
 id UUID PRIMARY KEY, resource_type TEXT NOT NULL CHECK(resource_type='file'), resource_id UUID NOT NULL,
 bot_id UUID NOT NULL, conv_id UUID NOT NULL, binding_id UUID NOT NULL, resource_binding_id UUID, owner_id UUID NOT NULL,
 subject_id UUID NOT NULL, subject_member_id UUID NOT NULL, scope TEXT NOT NULL CHECK(scope IN ('conversation','personal')),
 can_read BOOLEAN NOT NULL, can_share BOOLEAN NOT NULL, expires_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ,
 bot_authorization_version BIGINT NOT NULL,binding_authorization_version BIGINT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
 );
 CREATE TABLE knowledge.resource_grants (LIKE file.resource_grants INCLUDING ALL);
 ALTER TABLE knowledge.resource_grants DROP CONSTRAINT resource_grants_resource_type_check;
 ALTER TABLE knowledge.resource_grants ADD CHECK(resource_type='knowledge');
 CREATE INDEX ON file.resource_grants(bot_id,binding_id,resource_id);
 CREATE INDEX ON knowledge.resource_grants(bot_id,binding_id,resource_id);
 INSERT INTO public.schema_migrations(version) VALUES('010_resource_authorization') ON CONFLICT DO NOTHING;
END;
$resource_authorization$;
