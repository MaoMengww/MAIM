-- Recoverable evidence reclamation retains historical identities and outcomes,
-- never original Run messages, lawful business outputs, or workspaces.
DO $evidence_expiration$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 26);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '026_evidence_expiration') THEN
        RETURN;
    END IF;

    ALTER TABLE bot.run_reviews ADD COLUMN materials_cleared_at TIMESTAMPTZ;
    ALTER TABLE bot.run_reviews ADD COLUMN evidence_revoked_at TIMESTAMPTZ;
    CREATE INDEX idx_run_reviews_material_expiry ON bot.run_reviews(expires_at, id)
        WHERE materials_cleared_at IS NULL;

    -- Existing published contracts are execution/conclusion records, not a
    -- perpetual license to retain representative business samples or free text.
    UPDATE bot.skill_versions v SET validation_contract =
        COALESCE((SELECT jsonb_object_agg(key, value)
            FROM jsonb_each(v.validation_contract)
            WHERE key IN ('id', 'digest', 'baseline_version_id', 'scripts', 'image_digest',
                'entry_point', 'code_review_digest', 'code_review_identity', 'io_contract_digest')), '{}'::jsonb)
        || CASE WHEN jsonb_typeof(v.validation_contract->'governance') = 'object' THEN
            jsonb_build_object('governance', jsonb_build_object(
                'permissions', v.validation_contract->'governance'->'permissions',
                'data_scope', jsonb_build_object(
                    'scope', COALESCE(v.validation_contract->'governance'->'data_scope'->'scope', to_jsonb(v.scope_key)),
                    'source_authority_digest', '')))
            ELSE '{}'::jsonb END
        || CASE WHEN jsonb_typeof(v.validation_contract->'evaluation') = 'object' THEN
            jsonb_build_object('evaluation', COALESCE((SELECT jsonb_object_agg(key, value)
                FROM jsonb_each(v.validation_contract->'evaluation')
                WHERE key IN ('status', 'passed', 'digest', 'task_digest', 'expected_digest',
                    'output_digest', 'model_version', 'environment_digest', 'baseline_digest',
                    'evidence_digest', 'execution_id', 'code_review_identity', 'created_at')), '{}'::jsonb))
            ELSE '{}'::jsonb END;

    INSERT INTO public.schema_migrations(version) VALUES ('026_evidence_expiration') ON CONFLICT DO NOTHING;
END;
$evidence_expiration$;
