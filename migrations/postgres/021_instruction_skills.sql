-- Instruction skills: package identity, immutable versions, candidates, and Bot enablement.
DO $instruction_skills$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '021_instruction_skills') THEN
        RETURN;
    END IF;

    CREATE TABLE IF NOT EXISTS bot.skill_packages (
        id UUID PRIMARY KEY,
        bot_id UUID NOT NULL,
        publisher_id UUID NOT NULL,
        name TEXT NOT NULL,
        license TEXT NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE (bot_id, publisher_id, name)
    );

    CREATE TABLE IF NOT EXISTS bot.skill_versions (
        id UUID PRIMARY KEY,
        skill_id UUID NOT NULL REFERENCES bot.skill_packages(id),
        bot_id UUID NOT NULL,
        version INTEGER NOT NULL,
        digest TEXT NOT NULL,
        name TEXT NOT NULL,
        description TEXT NOT NULL,
        instructions TEXT NOT NULL,
        resources JSONB NOT NULL DEFAULT '{}'::jsonb,
        license TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('published','disabled')),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE (skill_id, version),
        UNIQUE (skill_id, digest)
    );

    CREATE TABLE IF NOT EXISTS bot.skill_candidates (
        id UUID PRIMARY KEY,
        skill_id UUID NOT NULL REFERENCES bot.skill_packages(id),
        bot_id UUID NOT NULL,
        digest TEXT NOT NULL,
        name TEXT NOT NULL,
        description TEXT NOT NULL,
        instructions TEXT NOT NULL,
        resources JSONB NOT NULL DEFAULT '{}'::jsonb,
        license TEXT NOT NULL,
        baseline_version_id UUID REFERENCES bot.skill_versions(id),
        status TEXT NOT NULL CHECK (status IN ('pending_validation','evaluating','evaluated','validation_failed','approved','rejected','published','expired')),
        task TEXT NOT NULL,
        expected_output TEXT NOT NULL,
        failure TEXT NOT NULL DEFAULT '',
        evaluation_digest TEXT NOT NULL DEFAULT '',
        approved_digest TEXT NOT NULL DEFAULT '',
        approved_evaluation_digest TEXT NOT NULL DEFAULT '',
        published_version_id UUID REFERENCES bot.skill_versions(id),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_candidates_identity
        ON bot.skill_candidates(skill_id, digest, COALESCE(baseline_version_id, '00000000-0000-0000-0000-000000000000'::uuid));

    CREATE TABLE IF NOT EXISTS bot.skill_evaluations (
        id UUID PRIMARY KEY,
        candidate_id UUID NOT NULL REFERENCES bot.skill_candidates(id),
        bot_id UUID NOT NULL,
        digest TEXT NOT NULL,
        status TEXT NOT NULL CHECK (status IN ('passed','failed','insufficient','error')),
        reason TEXT NOT NULL DEFAULT '',
        task_digest TEXT NOT NULL,
        expected_digest TEXT NOT NULL,
        output_digest TEXT NOT NULL DEFAULT '',
        observed TEXT NOT NULL DEFAULT '',
        model_version TEXT NOT NULL DEFAULT '',
        attempt INTEGER NOT NULL DEFAULT 1,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE (candidate_id, digest, attempt)
    );

    CREATE TABLE IF NOT EXISTS bot.skill_bindings (
        id UUID PRIMARY KEY,
        bot_id UUID NOT NULL,
        skill_id UUID NOT NULL REFERENCES bot.skill_packages(id),
        version_id UUID NOT NULL REFERENCES bot.skill_versions(id),
        enabled BOOLEAN NOT NULL DEFAULT TRUE,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE (bot_id, skill_id)
    );

    CREATE TABLE IF NOT EXISTS bot.skill_operations (
        id UUID PRIMARY KEY,
        bot_id UUID NOT NULL,
        operation TEXT NOT NULL,
        submission_key TEXT NOT NULL,
        request_hash TEXT NOT NULL,
        response JSONB NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        UNIQUE (bot_id, operation, submission_key)
    );

    ALTER TABLE bot.runs ADD COLUMN skill_loads JSONB NOT NULL DEFAULT '[]'::jsonb;

    INSERT INTO public.schema_migrations(version) VALUES ('021_instruction_skills') ON CONFLICT DO NOTHING;
END;
$instruction_skills$;
