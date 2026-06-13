-- Add UNIQUE constraint on (conv_id, seq) to prevent duplicate seq from any code path.
-- Drop the old non-unique index first (it's redundant with the unique constraint).

DROP INDEX IF EXISTS msg.idx_messages_conv_seq;

-- Use a unique index rather than a table-level constraint so we can CONCURRENTLY
-- in production if needed. CREATE UNIQUE INDEX CONCURRENTLY is safe for live tables.
CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_conv_seq ON msg.messages(conv_id, seq);
