-- Candidate governance: frozen execution review, durable validation identity and approval provenance.
DO $candidate_governance$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '023_candidate_governance') THEN
        RETURN;
    END IF;
    ALTER TABLE bot.skill_candidates DROP CONSTRAINT skill_candidates_status_check;
    ALTER TABLE bot.skill_candidates ADD CONSTRAINT skill_candidates_status_check CHECK (status IN ('pending_validation','awaiting_evidence','evaluating','evaluated','validation_failed','approved','rejected','published','expired'));
    ALTER TABLE bot.skill_candidates ADD COLUMN approved_by UUID;
    ALTER TABLE bot.skill_candidates ADD COLUMN approved_authorization_version BIGINT NOT NULL DEFAULT 0;
    ALTER TABLE bot.skill_versions ADD COLUMN validation_contract JSONB NOT NULL DEFAULT '{}'::jsonb;
    ALTER TABLE bot.skill_versions ADD COLUMN scope_key TEXT NOT NULL DEFAULT 'bot';
    ALTER TABLE bot.skill_bindings ADD COLUMN scope_key TEXT NOT NULL DEFAULT 'bot';
    ALTER TABLE bot.skill_bindings DROP CONSTRAINT skill_bindings_bot_id_skill_id_key;
    ALTER TABLE bot.skill_bindings ADD CONSTRAINT skill_bindings_scoped_identity UNIQUE(bot_id,skill_id,scope_key);
    ALTER TABLE bot.skill_evaluations ADD COLUMN content_digest TEXT NOT NULL DEFAULT '';
    UPDATE bot.skill_versions v SET validation_contract = jsonb_build_object('representative_task', c.task, 'expected_output', c.expected_output, 'baseline_version_id', c.baseline_version_id)
        FROM bot.skill_candidates c WHERE c.published_version_id = v.id AND c.source_review_id IS NULL;
    -- Old verdicts allowed substring matching and approvals lack provenance.
    -- Keep their audit rows, but require new exact verification and approval.
    UPDATE bot.skill_candidates SET status = 'pending_validation', evaluation_digest = '', approved_digest = '', approved_evaluation_digest = '' WHERE status IN ('evaluated','approved','validation_failed') AND source_review_id IS NULL;
    CREATE TABLE bot.skill_candidate_validations (
        candidate_id UUID NOT NULL REFERENCES bot.skill_candidates(id),
        digest TEXT NOT NULL,
        execution_key TEXT NOT NULL UNIQUE,
        status TEXT NOT NULL CHECK (status IN ('running','completed')),
        result JSONB NOT NULL DEFAULT '{}'::jsonb,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        PRIMARY KEY(candidate_id,digest)
    );
    INSERT INTO public.schema_migrations(version) VALUES ('023_candidate_governance') ON CONFLICT DO NOTHING;
END;
$candidate_governance$;
