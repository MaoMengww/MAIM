-- Durable, bot-owned work and publication; messaging remains the message authority.
DO $single_run$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '004_single_run') THEN
        RETURN;
    END IF;
    CREATE TABLE bot.interactions (
        id UUID PRIMARY KEY,
        bot_id UUID NOT NULL,
        conv_id UUID NOT NULL,
        binding_id UUID NOT NULL,
        requester_id UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    CREATE TABLE bot.runs (
        id UUID PRIMARY KEY,
        interaction_id UUID NOT NULL REFERENCES bot.interactions(id),
        bot_id UUID NOT NULL,
        conv_id UUID NOT NULL,
        binding_id UUID NOT NULL,
        requester_id UUID NOT NULL,
        input_key TEXT NOT NULL,
        input_hash TEXT NOT NULL,
        source_message_id UUID,
        reply_to_msg_id UUID,
        history_grant_id UUID,
        input_version BIGINT NOT NULL DEFAULT 1 CHECK (input_version > 0),
        version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
        trajectory_version BIGINT NOT NULL DEFAULT 0 CHECK (trajectory_version >= 0),
        execution_generation BIGINT NOT NULL DEFAULT 0,
        status TEXT NOT NULL CHECK (status IN ('accepted','running','succeeded','failed')),
        stage TEXT NOT NULL,
        result_text TEXT NOT NULL DEFAULT '',
        output_status TEXT NOT NULL DEFAULT 'none' CHECK (output_status IN ('none','generated','pending','committed','failed')),
        message_id UUID,
        error TEXT NOT NULL DEFAULT '',
        config JSONB NOT NULL,
        sources JSONB NOT NULL DEFAULT '[]',
        materials JSONB NOT NULL DEFAULT '[]',
        steps JSONB NOT NULL DEFAULT '[]',
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE(bot_id,binding_id,requester_id,input_key)
    );
    CREATE INDEX idx_runs_conversation ON bot.runs(conv_id,created_at DESC,id DESC);
    CREATE TABLE bot.run_inputs (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL REFERENCES bot.runs(id),
        version BIGINT NOT NULL CHECK (version > 0),
        requester_id UUID NOT NULL,
        source_message_id UUID,
        input_key TEXT NOT NULL,
        content TEXT NOT NULL,
        content_hash TEXT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE(run_id,version)
    );
    CREATE TABLE bot.run_trajectory (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL REFERENCES bot.runs(id),
        version BIGINT NOT NULL CHECK (version > 0),
        kind TEXT NOT NULL,
        block JSONB NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE(run_id,version)
    );
    CREATE TABLE bot.run_model_calls (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL REFERENCES bot.runs(id),
        interaction_id UUID NOT NULL,
        input_upper_bound BIGINT NOT NULL,
        trajectory_version BIGINT NOT NULL,
        execution_generation BIGINT NOT NULL,
        branch_version BIGINT NOT NULL DEFAULT 1,
        assembly_version TEXT NOT NULL,
        request JSONB NOT NULL,
        response JSONB,
        usage JSONB,
        first_token_latency_ms BIGINT,
        status TEXT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        completed_at TIMESTAMPTZ
    );
    CREATE TABLE bot.run_outbox (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL REFERENCES bot.runs(id),
        kind TEXT NOT NULL CHECK (kind IN ('execute','updated','output')),
        version BIGINT NOT NULL,
        payload JSONB NOT NULL DEFAULT '{}',
        attempts INTEGER NOT NULL DEFAULT 0,
        available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        lease_until TIMESTAMPTZ,
        lease_token UUID,
        completed_at TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE(run_id,kind,version)
    );
    CREATE INDEX idx_run_outbox_pending ON bot.run_outbox(available_at,created_at) WHERE completed_at IS NULL;
    INSERT INTO public.schema_migrations(version) VALUES ('004_single_run') ON CONFLICT DO NOTHING;
END;
$single_run$;
