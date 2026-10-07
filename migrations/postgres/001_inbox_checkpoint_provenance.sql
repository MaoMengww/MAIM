-- Keep the provenance of allocated checkpoints after inbox entry coalescing.
-- Existing UUID streams can recover provenance only for entries still present;
-- never infer unknown positions or recreate removed business data.
ALTER TABLE messaging.inbox_applied_changes
    ADD COLUMN IF NOT EXISTS position BIGINT CHECK (position BETWEEN 1 AND 9007199254740991),
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

INSERT INTO messaging.inbox_applied_changes (user_id, change_id, position, created_at)
SELECT user_id, change_id, position, created_at FROM messaging.inbox_entries
ON CONFLICT (user_id, change_id) DO UPDATE
SET position = EXCLUDED.position, created_at = EXCLUDED.created_at
WHERE messaging.inbox_applied_changes.position IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_inbox_applied_position
    ON messaging.inbox_applied_changes(user_id, position) WHERE position IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_inbox_applied_created
    ON messaging.inbox_applied_changes(created_at) WHERE position IS NOT NULL;
