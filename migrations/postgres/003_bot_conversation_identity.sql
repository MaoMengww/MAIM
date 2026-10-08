-- Bot participation is an independent binding lifecycle, not human membership.
-- Applies after the UUID baseline on both new and existing databases.
DO $bot_conversation_identity$
DECLARE
    event_table TEXT;
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (
        SELECT 1 FROM public.schema_migrations
        WHERE version = '003_bot_conversation_identity'
    ) THEN
        RETURN;
    END IF;

    ALTER TABLE messaging.conv_bots ADD COLUMN revoked_at TIMESTAMPTZ;
    ALTER TABLE messaging.conv_bots ADD COLUMN start_seq BIGINT NOT NULL DEFAULT 0
        CHECK (start_seq BETWEEN 0 AND 9007199254740991);
    -- Cut over pending activity at the same committed sequence boundary as sends.
    UPDATE messaging.conv_bots b SET start_seq = c.max_seq
        FROM messaging.conversations c WHERE c.id = b.conv_id;
    DROP INDEX messaging.idx_conv_bots_pair;
    CREATE UNIQUE INDEX idx_conv_bots_active_pair
        ON messaging.conv_bots(conv_id, bot_id) WHERE revoked_at IS NULL;

    -- Remove only legacy Bot membership rows; retain all human relationships.
    DELETE FROM messaging.conv_members WHERE member_type = 'bot';
    ALTER TABLE messaging.conv_members DROP CONSTRAINT conv_members_identity;
    DROP INDEX messaging.idx_conv_members_bot_pair;
    ALTER TABLE messaging.conv_members DROP COLUMN bot_id;
    ALTER TABLE messaging.conv_members ALTER COLUMN user_id SET NOT NULL;
    ALTER TABLE messaging.conv_members ADD CONSTRAINT conv_members_identity
        CHECK (member_type = 'user');
    UPDATE messaging.conversations c
       SET member_count = (
           SELECT COUNT(*) FROM messaging.conv_members m WHERE m.conv_id = c.id
       );

    ALTER TABLE messaging.messages
        ADD COLUMN sender_name TEXT NOT NULL DEFAULT '',
        ADD COLUMN sender_avatar TEXT NOT NULL DEFAULT '',
        ADD COLUMN binding_id UUID CHECK (binding_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        ADD COLUMN requester_id UUID CHECK (requester_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        ADD COLUMN raw_payload TEXT NOT NULL DEFAULT '';

    -- Keep the recorded sender_id / sender_type as the identity authority.
    -- The old payload only supplies display snapshots and executor output data.
    -- No current binding or inviter can establish the requester/lifecycle of an
    -- old message, so binding_id and requester_id remain NULL for those rows.
    UPDATE messaging.messages
       SET sender_name = COALESCE(content->>'bot_name', ''),
           sender_avatar = COALESCE(content->>'bot_avatar', ''),
           raw_payload = COALESCE(content->>'raw_payload', '')
     WHERE msg_type = 9;
    UPDATE messaging.messages
       SET msg_type = 1,
           content = jsonb_build_object(
               'text', COALESCE(content->>'text', ''),
               'mention_user_ids', '[]'::jsonb,
               'mention_all', false
           )
     WHERE msg_type = 9;
    ALTER TABLE messaging.messages ADD CONSTRAINT messages_content_not_bot
        CHECK (msg_type <> 9);

    -- Pending and failed publications must replay the same new content contract
    -- as history; keep their original IDs, ordering, status and sender identity.
    FOR event_table IN SELECT unnest(ARRAY['outbox_events', 'failed_events']) LOOP
        EXECUTE format($convert_event$
            UPDATE messaging.%I
               SET payload = payload || jsonb_build_object(
                   'msg_type', 1,
                   'sender_name', COALESCE(NULLIF(payload->>'sender_name', ''), payload->'content'->>'bot_name', ''),
                   'sender_avatar', COALESCE(NULLIF(payload->>'sender_avatar', ''), payload->'content'->>'bot_avatar', ''),
                   'raw_payload', COALESCE(NULLIF(payload->>'raw_payload', ''), payload->'content'->>'raw_payload', ''),
                   'content', jsonb_build_object(
                       'text', COALESCE(payload->'content'->>'text', ''),
                       'mention_user_ids', '[]'::jsonb,
                       'mention_all', false
                   )
               )
             WHERE payload->>'msg_type' = '9'
        $convert_event$, event_table);
    END LOOP;

    CREATE TABLE messaging.bot_history_grants (
        id UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
        binding_id UUID NOT NULL REFERENCES messaging.conv_bots(id)
            CHECK (binding_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        granted_by UUID NOT NULL CHECK (granted_by <> '00000000-0000-0000-0000-000000000000'::uuid),
        start_seq BIGINT NOT NULL CHECK (start_seq BETWEEN 1 AND 9007199254740991),
        end_seq BIGINT NOT NULL CHECK (end_seq BETWEEN 1 AND 9007199254740991),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        CONSTRAINT bot_history_grants_range CHECK (start_seq <= end_seq),
        CONSTRAINT bot_history_grants_binding UNIQUE (id, binding_id)
    );
    CREATE INDEX idx_bot_history_grants_binding
        ON messaging.bot_history_grants(binding_id, created_at);

    CREATE TABLE messaging.bot_history_reads (
        id UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
        grant_id UUID NOT NULL CHECK (grant_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        binding_id UUID NOT NULL CHECK (binding_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        start_seq BIGINT NOT NULL CHECK (start_seq BETWEEN 1 AND 9007199254740991),
        end_seq BIGINT NOT NULL CHECK (end_seq BETWEEN 1 AND 9007199254740991),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        CONSTRAINT bot_history_reads_range CHECK (start_seq <= end_seq),
        CONSTRAINT bot_history_reads_grant_binding FOREIGN KEY (grant_id, binding_id)
            REFERENCES messaging.bot_history_grants(id, binding_id)
    );
    CREATE INDEX idx_bot_history_reads_grant
        ON messaging.bot_history_reads(grant_id, created_at);

    INSERT INTO public.schema_migrations(version)
        VALUES ('003_bot_conversation_identity') ON CONFLICT DO NOTHING;
END;
$bot_conversation_identity$;
