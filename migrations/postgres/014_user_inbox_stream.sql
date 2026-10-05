-- The allocator and entries commit together. Readers may use the allocator's
-- position as the committed visibility boundary; no smaller position can appear
-- later because writers hold its row lock through entry insertion and commit.
CREATE TABLE IF NOT EXISTS messaging.inbox_streams (
    user_id  BIGINT PRIMARY KEY CHECK (user_id > 0),
    position BIGINT NOT NULL DEFAULT 0 CHECK (position >= 0)
);

CREATE TABLE IF NOT EXISTS messaging.inbox_entries (
    user_id    BIGINT NOT NULL REFERENCES messaging.inbox_streams(user_id) ON DELETE CASCADE,
    position   BIGINT NOT NULL CHECK (position > 0),
    conv_id    BIGINT NOT NULL REFERENCES messaging.conversations(id) ON DELETE CASCADE CHECK (conv_id > 0),
    message_id BIGINT NOT NULL CHECK (message_id > 0),
    kind       TEXT NOT NULL CHECK (kind <> ''),
    -- Personal deletion remains here until issue09 moves it to its own overlay.
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, position),
    UNIQUE (user_id, conv_id, message_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_inbox_entries_created ON messaging.inbox_entries(created_at);
CREATE INDEX IF NOT EXISTS idx_inbox_entries_conv ON messaging.inbox_entries(user_id, conv_id);

-- Preserve valid message references on upgrade; conversationless broadcasts and
-- dangling references are not changes in a conversation and cannot be migrated.
-- Compose also executes migrations without recording their version, so this
-- block must tolerate another startup migration pass.
DO $$
BEGIN
    IF to_regclass('messaging.user_inbox') IS NOT NULL THEN
        INSERT INTO messaging.inbox_streams (user_id)
        SELECT DISTINCT old.user_id
        FROM messaging.user_inbox old
        JOIN messaging.conversations conv ON conv.id = old.conv_id
        JOIN messaging.messages msg ON msg.id = old.message_id AND msg.conv_id = old.conv_id
        WHERE old.user_id > 0 AND old.conv_id > 0 AND old.message_id > 0
        ON CONFLICT DO NOTHING;

        INSERT INTO messaging.inbox_entries
            (user_id, position, conv_id, message_id, kind, is_deleted, created_at)
        SELECT old.user_id,
               stream.position + ROW_NUMBER() OVER (
                   PARTITION BY old.user_id ORDER BY old.created_at, old.conv_id, old.seq, old.message_id),
               old.conv_id, old.message_id, 'message.new', old.is_deleted, old.created_at
        FROM (
            SELECT DISTINCT ON (old.user_id, old.conv_id, old.message_id) old.*
            FROM messaging.user_inbox old
            JOIN messaging.conversations conv ON conv.id = old.conv_id
            JOIN messaging.messages msg ON msg.id = old.message_id AND msg.conv_id = old.conv_id
            WHERE old.user_id > 0 AND old.conv_id > 0 AND old.message_id > 0
            ORDER BY old.user_id, old.conv_id, old.message_id, old.created_at, old.seq
        ) old
        JOIN messaging.inbox_streams stream ON stream.user_id = old.user_id
        ON CONFLICT (user_id, conv_id, message_id, kind) DO NOTHING;

        UPDATE messaging.inbox_streams stream
        SET position = GREATEST(stream.position, latest.position)
        FROM (SELECT user_id, MAX(position) AS position FROM messaging.inbox_entries GROUP BY user_id) latest
        WHERE stream.user_id = latest.user_id;

        DROP TABLE messaging.user_inbox;
    END IF;
END $$;
