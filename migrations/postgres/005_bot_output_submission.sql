-- A durable Bot publication receipt belongs to messaging, not execution state.
-- It commits with the message, conversation sequence/tail and message outbox.
DO $bot_output_submission$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (
        SELECT 1 FROM public.schema_migrations
        WHERE version = '005_bot_output_submission'
    ) THEN
        RETURN;
    END IF;

    CREATE TABLE messaging.bot_output_submissions (
        bot_id UUID NOT NULL CHECK (bot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        binding_id UUID NOT NULL REFERENCES messaging.conv_bots(id)
            CHECK (binding_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        output_key TEXT NOT NULL CHECK (octet_length(output_key) BETWEEN 1 AND 512),
        conv_id UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        content_fingerprint JSONB NOT NULL CHECK (jsonb_typeof(content_fingerprint) = 'object'),
        message_id UUID NOT NULL CHECK (message_id <> '00000000-0000-0000-0000-000000000000'::uuid),
        seq BIGINT NOT NULL CHECK (seq BETWEEN 1 AND 9007199254740991),
        created_at TIMESTAMPTZ NOT NULL,
        PRIMARY KEY (bot_id, binding_id, output_key),
        UNIQUE (message_id)
    );
    -- The immutable receipt remains after later message visibility changes.
    -- No Run FK or executor data is stored in messaging's publication mapping.
    CREATE INDEX idx_bot_output_submissions_conversation
        ON messaging.bot_output_submissions(conv_id, seq);

    INSERT INTO public.schema_migrations(version)
        VALUES ('005_bot_output_submission') ON CONFLICT DO NOTHING;
END;
$bot_output_submission$;
