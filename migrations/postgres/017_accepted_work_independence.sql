-- Accepted work has its own lifecycle. Retire only the old message-visibility
-- coupling; provenance, accepted versions and real authorization fences remain.
DO $accepted_work_independence$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '017_accepted_work_independence') THEN RETURN; END IF;
    IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '016_action_source_validity') THEN RAISE EXCEPTION '016_action_source_validity must be applied before 017_accepted_work_independence'; END IF;

    PERFORM id FROM messaging.conversations ORDER BY id FOR UPDATE;

    -- Historical accepted materials normally already contain seq. Recover only
    -- missing sequence metadata from an existing same-conversation source row;
    -- never infer it from a grant's range or restore a cleared material body.
    -- A deleted source with no accepted seq remains unavailable for grant checks.
    UPDATE bot.runs run SET materials = (
        SELECT jsonb_agg(CASE WHEN message.id IS NOT NULL
            AND (material.value->'seq' IS NULL OR material.value->'seq' IN ('null'::jsonb, '0'::jsonb))
            THEN material.value || jsonb_build_object('seq', message.seq)
            ELSE material.value END ORDER BY material.ordinality)
        FROM jsonb_array_elements(run.materials) WITH ORDINALITY AS material(value, ordinality)
        LEFT JOIN messaging.messages message ON message.id::text = material.value->>'message_id'
            AND message.conv_id = run.conv_id
    )
    WHERE EXISTS (
        SELECT 1 FROM jsonb_array_elements(run.materials) material(value)
        JOIN messaging.messages message ON message.id::text = material.value->>'message_id'
            AND message.conv_id = run.conv_id
        WHERE material.value->'seq' IS NULL OR material.value->'seq' IN ('null'::jsonb, '0'::jsonb)
    );

    UPDATE bot.run_inputs input SET materials = (
        SELECT jsonb_agg(CASE WHEN message.id IS NOT NULL
            AND (material.value->'seq' IS NULL OR material.value->'seq' IN ('null'::jsonb, '0'::jsonb))
            THEN material.value || jsonb_build_object('seq', message.seq)
            ELSE material.value END ORDER BY material.ordinality)
        FROM jsonb_array_elements(input.materials) WITH ORDINALITY AS material(value, ordinality)
        LEFT JOIN messaging.messages message ON message.id::text = material.value->>'message_id'
            AND message.conv_id = run.conv_id
    )
    FROM bot.runs run
    WHERE input.run_id = run.id AND EXISTS (
        SELECT 1 FROM jsonb_array_elements(input.materials) material(value)
        JOIN messaging.messages message ON message.id::text = material.value->>'message_id'
            AND message.conv_id = run.conv_id
        WHERE material.value->'seq' IS NULL OR material.value->'seq' IN ('null'::jsonb, '0'::jsonb)
    );

    DROP TABLE IF EXISTS messaging.message_source_refs;
    ALTER TABLE bot.runs DROP COLUMN IF EXISTS source_changes;

    -- A historical reconfirmation pause remains paused, but no longer advertises
    -- a special source-confirmation gate. Do not resume or rebuild cleared input.
    UPDATE bot.runs SET stage = status
    WHERE stage = 'source_confirmation_required';
    UPDATE bot.runs SET waiting_reason = 'Previously cleared work input is unavailable; provide new input to continue.'
    WHERE waiting_reason IN (
        'Source material is no longer available; explicit input is required.',
        'Source versions changed; submit explicit input to reconfirm the accepted constraints.'
    );
    -- The old notice also represented real resource revocation. Preserve that
    -- historical removal fact without retaining the retired reconfirmation rule.
    UPDATE bot.runs SET context_notice = 'Legacy source reconciliation removed material and dependent context; cleared content cannot be restored.'
    WHERE context_notice = 'Unavailable source material and dependent context were removed; submit explicit input to reconfirm the remaining constraints.';

    -- Keep every personal_message_deletions row: historical derived overlays
    -- cannot be distinguished reliably from deliberate account deletions.
    -- Keep invalidation flags and scrubbed records as historical facts: old
    -- logic may have permanently cleared bodies, approvals or recovery state.
    -- Do not fabricate their restoration or revive previously stopped work.
    INSERT INTO public.schema_migrations(version) VALUES ('017_accepted_work_independence') ON CONFLICT DO NOTHING;
END;
$accepted_work_independence$;
