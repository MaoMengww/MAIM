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
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, position),
    UNIQUE (user_id, conv_id, message_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_inbox_entries_created ON messaging.inbox_entries(created_at);
CREATE INDEX IF NOT EXISTS idx_inbox_entries_conv ON messaging.inbox_entries(user_id, conv_id);

