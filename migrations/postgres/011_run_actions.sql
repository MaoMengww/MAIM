DO $run_actions$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '011_run_actions') THEN RETURN; END IF;
    IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '010_resource_authorization') THEN
        RAISE EXCEPTION '010_resource_authorization must be applied before 011_run_actions';
    END IF;
    CREATE TABLE bot.run_actions (
        id UUID PRIMARY KEY, run_id UUID NOT NULL REFERENCES bot.runs(id) ON DELETE CASCADE,
        bot_id UUID NOT NULL, binding_id UUID NOT NULL, requester_id UUID NOT NULL,
        controller_id UUID NOT NULL, controller_member_id UUID NOT NULL,
        server_id TEXT NOT NULL, tool_name TEXT NOT NULL, definition_hash TEXT NOT NULL,
        parameters_hash TEXT NOT NULL, arguments JSONB NOT NULL, tool_definition JSONB NOT NULL, target TEXT NOT NULL,
        risk TEXT NOT NULL, submission_key TEXT NOT NULL, generation BIGINT NOT NULL,
        input_version BIGINT NOT NULL, status TEXT NOT NULL, approver_id TEXT NOT NULL DEFAULT '',
        grant_id UUID, expires_at TIMESTAMPTZ NOT NULL, claimed_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
        result TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE(run_id, submission_key)
    );
    CREATE TABLE bot.action_grants (
        id UUID PRIMARY KEY, bot_id UUID NOT NULL, conv_id UUID NOT NULL, binding_id UUID NOT NULL,
        owner_id UUID NOT NULL, requester_id UUID NOT NULL, server_id TEXT NOT NULL, tool_name TEXT NOT NULL,
        subject_member_id UUID NOT NULL,
        definition_hash TEXT NOT NULL, parameters_hash TEXT NOT NULL,
        bot_authorization_version BIGINT NOT NULL, binding_authorization_version BIGINT NOT NULL,
        expires_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    CREATE INDEX action_grant_match ON bot.action_grants(bot_id,binding_id,server_id,tool_name,parameters_hash);
    CREATE INDEX run_action_status ON bot.run_actions(run_id,status);
    INSERT INTO public.schema_migrations(version) VALUES ('011_run_actions') ON CONFLICT DO NOTHING;
END;
$run_actions$;
