-- Account deletion state survives inbox collection and message deletion.
CREATE TABLE IF NOT EXISTS messaging.personal_message_deletions (
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    conv_id BIGINT NOT NULL CHECK (conv_id > 0),
    message_id BIGINT NOT NULL CHECK (message_id > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, conv_id, message_id)
);

-- Development databases may still have the earlier inbox-local deletion flag.
-- Compose executes migrations repeatedly, so migrate and drop it only once.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'messaging' AND table_name = 'inbox_entries' AND column_name = 'is_deleted') THEN
        INSERT INTO messaging.personal_message_deletions (user_id, conv_id, message_id, created_at)
        SELECT user_id, conv_id, message_id, MIN(created_at)
        FROM messaging.inbox_entries
        WHERE is_deleted AND message_id > 0
        GROUP BY user_id, conv_id, message_id
        ON CONFLICT DO NOTHING;
        ALTER TABLE messaging.inbox_entries DROP COLUMN is_deleted;
    END IF;
END $$;
