-- Action arguments/results are source-derived copies, not independent public
-- facts. Preserve actual execution status while permanently fencing old input.
DO $action_source_validity$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '016_action_source_validity') THEN RETURN; END IF;
    IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '015_derived_message_visibility') THEN RAISE EXCEPTION '015_derived_message_visibility must be applied before 016_action_source_validity'; END IF;

    PERFORM id FROM messaging.conversations ORDER BY id FOR UPDATE;
    ALTER TABLE bot.run_actions
        ADD COLUMN provenance JSONB NOT NULL DEFAULT '[]',
        ADD COLUMN source_invalidated BOOLEAN NOT NULL DEFAULT FALSE;

    -- Prefer the durable provider snapshot for the action's generation and
    -- accepted input watermark. Legacy fallback uses identity/version provenance
    -- only; never infer trusted sources from arguments or external results.
    UPDATE bot.run_actions action SET provenance = COALESCE(
        (SELECT call.provenance FROM bot.run_model_calls call
         WHERE call.run_id = action.run_id AND call.execution_generation = action.generation
           AND call.input_upper_bound = action.input_version AND call.created_at <= action.created_at
         ORDER BY call.created_at DESC, call.id DESC LIMIT 1),
        (SELECT COALESCE(jsonb_agg(DISTINCT source.value), '[]'::jsonb) FROM (
            SELECT item.value FROM bot.run_inputs input
            CROSS JOIN LATERAL jsonb_array_elements(input.provenance) item(value)
            WHERE input.run_id = action.run_id AND input.version <= action.input_version
            UNION
            SELECT jsonb_build_object('message_id', input.source_message_id,
                'accepted_edit_count', input.accepted_edit_count)
            FROM bot.run_inputs input WHERE input.run_id = action.run_id
                AND input.version <= action.input_version AND input.source_message_id IS NOT NULL
            UNION
            SELECT item.value FROM bot.runs run
            CROSS JOIN LATERAL jsonb_array_elements(run.provenance) item(value)
            WHERE run.id = action.run_id AND run.input_version = action.input_version
            UNION
            SELECT item.value FROM bot.run_trajectory trajectory
            CROSS JOIN LATERAL jsonb_array_elements(trajectory.provenance) item(value)
            WHERE trajectory.run_id = action.run_id AND trajectory.created_at <= action.created_at
        ) source(value)))
    WHERE TRUE;

    UPDATE bot.run_actions action SET source_invalidated = TRUE,
        arguments = '{}'::jsonb, target = '', result = ''
    FROM bot.runs run
    WHERE run.id = action.run_id AND EXISTS (
        SELECT 1 FROM jsonb_array_elements(action.provenance) source(value)
        LEFT JOIN messaging.messages message ON message.id = NULLIF(source.value->>'message_id', '')::uuid
            AND message.conv_id = run.conv_id
        LEFT JOIN bot.runs parent ON parent.id = NULLIF(source.value->>'parent_run_id', '')::uuid
        WHERE (COALESCE(source.value->>'message_id', '') <> '' AND (
            message.id IS NULL OR message.status = 2
            OR EXISTS (SELECT 1 FROM jsonb_array_elements(run.source_changes) change(value)
                WHERE change.value->>'message_id' = source.value->>'message_id'
                  AND change.value->>'status' IN ('withdrawn', 'unavailable'))
            OR (run.visibility = 'personal' AND EXISTS (
                SELECT 1 FROM messaging.personal_message_deletions deleted
                WHERE deleted.conv_id = run.conv_id AND deleted.user_id = run.personal_user_id
                  AND deleted.message_id = message.id))))
            OR (COALESCE(source.value->>'parent_run_id', '') <> '' AND (
                parent.id IS NULL OR parent.access_revoked
                OR parent.branch_version <> COALESCE((source.value->>'source_revision')::bigint, 1)))
    );

    INSERT INTO public.schema_migrations(version) VALUES ('016_action_source_validity') ON CONFLICT DO NOTHING;
END;
$action_source_validity$;
