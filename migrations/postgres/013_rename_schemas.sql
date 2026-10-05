-- P7: rename the messaging/realtime schemas to their final names.
--
-- Databases created before P7 still carry the old msg / notify schema names.
-- ALTER SCHEMA RENAME moves every contained table, sequence, index and
-- constraint in place without copying rows or changing object identity, so no
-- data is lost and no table is dropped. The block is idempotent: a missing
-- source or an already-present destination is skipped, and an ambiguous
-- both-exist state is left untouched for manual reconciliation.
--
-- On a pre-P7 database this must run before 000 recreates the destination
-- schema, otherwise the empty destination would shadow the existing msg/notify
-- schemas.
DO $$
BEGIN
    -- Share the runner's lock, including when this SQL is executed directly.
    PERFORM pg_advisory_xact_lock(4278605, 1);

    IF to_regnamespace('msg') IS NOT NULL AND to_regnamespace('messaging') IS NULL THEN
        ALTER SCHEMA msg RENAME TO messaging;
    END IF;

    IF to_regnamespace('notify') IS NOT NULL AND to_regnamespace('realtime') IS NULL THEN
        ALTER SCHEMA notify RENAME TO realtime;
    END IF;
END
$$;
