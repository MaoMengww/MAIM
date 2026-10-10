-- Frozen published-message identity provenance belongs to messaging. Personal
-- deletion remains an account overlay; no shared Run or result is withdrawn.
DO $derived_message_visibility$
DECLARE
    repaired RECORD;
    change_id UUID;
    publication_sequence BIGINT;
    random_bits TEXT;
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '015_derived_message_visibility') THEN RETURN; END IF;
    IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '014_work_source_validity') THEN RAISE EXCEPTION '014_work_source_validity must be applied before 015_derived_message_visibility'; END IF;

    -- The same conversation-first barrier as publication and personal deletion
    -- also fences canonical Run writers while the legacy graph is frozen.
    PERFORM id FROM messaging.conversations ORDER BY id FOR UPDATE;

    CREATE TABLE messaging.message_source_refs (
        message_id UUID NOT NULL REFERENCES messaging.messages(id) ON DELETE CASCADE
            CHECK (message_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        source_message_id UUID NOT NULL
            CHECK (source_message_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        conv_id UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        PRIMARY KEY (message_id, source_message_id),
        CHECK (message_id <> source_message_id)
    );
    -- A source intentionally has no FK: deletion cannot erase inherited privacy.
    CREATE INDEX idx_message_source_refs_source
        ON messaging.message_source_refs(conv_id, source_message_id, message_id);

    WITH RECURSIVE
    run_links(run_id, parent_id, conv_id) AS (
        SELECT id, parent_run_id, conv_id FROM bot.runs WHERE parent_run_id IS NOT NULL
        UNION
        SELECT run.id, NULLIF(source.value->>'parent_run_id', '')::uuid, run.conv_id
        FROM bot.runs run CROSS JOIN LATERAL jsonb_array_elements(run.provenance) AS source(value)
        WHERE COALESCE(source.value->>'parent_run_id', '') <> ''
        UNION
        SELECT input.run_id, NULLIF(source.value->>'parent_run_id', '')::uuid, run.conv_id
        FROM bot.run_inputs input JOIN bot.runs run ON run.id = input.run_id
        CROSS JOIN LATERAL jsonb_array_elements(input.provenance) AS source(value)
        WHERE COALESCE(source.value->>'parent_run_id', '') <> ''
    ),
    ancestors(run_id, ancestor_id, conv_id) AS (
        SELECT id, id, conv_id FROM bot.runs
        UNION
        SELECT chain.run_id, link.parent_id, chain.conv_id FROM ancestors chain
        JOIN run_links link ON link.run_id = chain.ancestor_id AND link.conv_id = chain.conv_id
        JOIN bot.runs parent ON parent.id = link.parent_id AND parent.conv_id = chain.conv_id
    ),
    run_outputs(run_id, message_id, conv_id) AS (
        SELECT id, message_id, conv_id FROM bot.runs WHERE message_id IS NOT NULL
        UNION
        -- Receipt identity survives the messaging/Bot completion crash window.
        SELECT run.id, receipt.message_id, run.conv_id FROM bot.runs run
        JOIN messaging.bot_output_submissions receipt ON receipt.bot_id = run.bot_id
            AND receipt.binding_id = run.binding_id AND receipt.conv_id = run.conv_id
            AND receipt.output_key = run.id::text || ':final'
    ),
    run_sources(run_id, source_message_id) AS (
        SELECT id, source_message_id FROM bot.runs WHERE source_message_id IS NOT NULL
        UNION
        SELECT run_id, source_message_id FROM bot.run_inputs WHERE source_message_id IS NOT NULL
        UNION
        SELECT run.id, NULLIF(source.value->>'message_id', '')::uuid FROM bot.runs run
        CROSS JOIN LATERAL jsonb_array_elements(run.provenance) AS source(value)
        WHERE COALESCE(source.value->>'message_id', '') <> ''
        UNION
        SELECT input.run_id, NULLIF(source.value->>'message_id', '')::uuid FROM bot.run_inputs input
        CROSS JOIN LATERAL jsonb_array_elements(input.provenance) AS source(value)
        WHERE COALESCE(source.value->>'message_id', '') <> ''
    ),
    direct_refs(message_id, source_message_id, conv_id) AS (
        SELECT output.message_id, source.source_message_id, output.conv_id
        FROM run_outputs output JOIN ancestors chain ON chain.run_id = output.run_id
        JOIN run_sources source ON source.run_id = chain.ancestor_id
        UNION
        SELECT output.message_id, parent_output.message_id, output.conv_id
        FROM run_outputs output JOIN ancestors chain ON chain.run_id = output.run_id
        JOIN run_outputs parent_output ON parent_output.run_id = chain.ancestor_id
        JOIN bot.runs parent ON parent.id = chain.ancestor_id AND parent.status IN ('succeeded', 'failed', 'stopped')
        WHERE chain.ancestor_id <> chain.run_id
        UNION
        -- Independently published Bot replies have a validated AIM target, not
        -- trusted raw_payload metadata. Those without a Run/target have no sources.
        SELECT id, reply_to_msg_id, conv_id FROM messaging.messages
        WHERE sender_type = 'bot' AND reply_to_msg_id IS NOT NULL
    ),
    frozen(message_id, source_message_id, conv_id) AS (
        SELECT message_id, source_message_id, conv_id FROM direct_refs WHERE message_id <> source_message_id
        UNION
        SELECT derived.message_id, inherited.source_message_id, derived.conv_id
        FROM frozen derived JOIN direct_refs inherited ON inherited.message_id = derived.source_message_id
            AND inherited.conv_id = derived.conv_id
        WHERE derived.message_id <> inherited.source_message_id
    )
    INSERT INTO messaging.message_source_refs(message_id, source_message_id, conv_id)
    SELECT frozen.message_id, frozen.source_message_id, frozen.conv_id FROM frozen
    JOIN messaging.messages message ON message.id = frozen.message_id AND message.conv_id = frozen.conv_id
    WHERE message.sender_type = 'bot'
    ON CONFLICT DO NOTHING;

    -- Repair existing account reads AND cached devices through the ordinary
    -- ordered outbox. The consumer owns inbox checkpoint allocation/deduplication.
    FOR repaired IN
        WITH hidden AS (
            INSERT INTO messaging.personal_message_deletions(user_id, conv_id, message_id)
            SELECT DISTINCT deleted.user_id, refs.conv_id, refs.message_id
            FROM messaging.message_source_refs refs
            JOIN messaging.personal_message_deletions deleted ON deleted.conv_id = refs.conv_id
                AND deleted.message_id = refs.source_message_id
            ON CONFLICT DO NOTHING
            RETURNING user_id, conv_id, message_id
        )
        SELECT hidden.* FROM hidden JOIN messaging.messages message ON message.id = hidden.message_id
        ORDER BY hidden.conv_id, message.seq, hidden.message_id, hidden.user_id
    LOOP
        UPDATE messaging.conversations SET publication_sequence = messaging.conversations.publication_sequence + 1
        WHERE id = repaired.conv_id RETURNING messaging.conversations.publication_sequence INTO publication_sequence;
        -- PostgreSQL 16 has no UUIDv7 function. This one-time domain migration
        -- uses the RFC timestamp/random layout, not a DB identity default/sequence.
        random_bits := replace(gen_random_uuid()::text, '-', '');
        change_id := (lpad(to_hex(floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint), 12, '0')
            || '7' || substr(random_bits, 14, 3) || substr(random_bits, 17, 16))::uuid;
        INSERT INTO messaging.outbox_events(id, conv_id, publication_sequence, topic, key, payload)
        VALUES (change_id, repaired.conv_id, publication_sequence, 'message.created', repaired.conv_id::text,
            jsonb_build_object('kind', 'message.deleted', 'conv_id', repaired.conv_id,
                'message_id', repaired.message_id, 'user_id', repaired.user_id,
                'recipient_ids', jsonb_build_array(repaired.user_id), 'change_id', change_id,
                'publication_sequence', publication_sequence));
    END LOOP;

    INSERT INTO public.schema_migrations(version) VALUES ('015_derived_message_visibility') ON CONFLICT DO NOTHING;
END;
$derived_message_visibility$;
