-- Retention invalidates only the removed prefix; the committed end never resets.
ALTER TABLE messaging.inbox_streams
    ADD COLUMN IF NOT EXISTS retained_position BIGINT NOT NULL DEFAULT 0
    CHECK (retained_position >= 0 AND retained_position <= position);
