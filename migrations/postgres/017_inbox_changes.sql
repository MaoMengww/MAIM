-- References must outlive a deleted conversation/message so removed/tombstone changes replay.
ALTER TABLE messaging.inbox_entries DROP CONSTRAINT IF EXISTS inbox_entries_conv_id_fkey;
ALTER TABLE messaging.inbox_entries DROP CONSTRAINT IF EXISTS inbox_entries_message_id_check;
ALTER TABLE messaging.inbox_entries DROP CONSTRAINT IF EXISTS inbox_entries_user_id_conv_id_message_id_kind_key;
ALTER TABLE messaging.inbox_entries ALTER COLUMN message_id SET DEFAULT 0;
ALTER TABLE messaging.inbox_entries ADD COLUMN IF NOT EXISTS change_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE messaging.inbox_entries ADD COLUMN IF NOT EXISTS last_read_seq BIGINT NOT NULL DEFAULT 0;
UPDATE messaging.inbox_entries SET change_id = message_id WHERE change_id = 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_inbox_change_id ON messaging.inbox_entries(user_id, change_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_inbox_own_read ON messaging.inbox_entries(user_id, conv_id, kind) WHERE kind = 'read.updated';

-- Read coalescing and retention must not make a consumed event replay allocate another position.
CREATE TABLE IF NOT EXISTS messaging.inbox_applied_changes (
    user_id BIGINT NOT NULL REFERENCES messaging.inbox_streams(user_id) ON DELETE CASCADE,
    change_id BIGINT NOT NULL CHECK (change_id > 0),
    PRIMARY KEY (user_id, change_id)
);
INSERT INTO messaging.inbox_applied_changes(user_id, change_id)
SELECT user_id, change_id FROM messaging.inbox_entries ON CONFLICT DO NOTHING;
