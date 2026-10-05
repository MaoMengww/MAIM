-- P4: an offline, atomic schema move. Stop the old conversation service first.
-- ALTER TABLE moves its indexes, constraints and owned sequences without copying
-- rows or changing object identity. Ambiguous source/destination copies abort;
-- never pick a winner or silently discard one side of a conflict.
-- P7: the destination schema was renamed msg -> messaging, so the names below
-- follow the current schema; 013 migrates pre-P7 databases (msg -> messaging).
DO $$
DECLARE
    t TEXT;
    source_table REGCLASS;
    target_table REGCLASS;
BEGIN
    -- Share the runner's lock, including when this SQL is executed directly.
    PERFORM pg_advisory_xact_lock(4278605, 1);
    CREATE SCHEMA IF NOT EXISTS messaging;

    FOREACH t IN ARRAY ARRAY['conversations', 'conv_members', 'conv_read_seqs', 'conv_settings', 'conv_bots'] LOOP
        source_table := to_regclass(format('conv.%I', t));
        target_table := to_regclass(format('messaging.%I', t));
        IF source_table IS NOT NULL AND target_table IS NOT NULL THEN
            RAISE EXCEPTION 'P4 schema conflict: both conv.% and messaging.% exist; reconcile them before migrating', t, t;
        END IF;
        IF source_table IS NULL AND target_table IS NULL THEN
            RAISE EXCEPTION 'P4 missing conversation table: neither conv.% nor messaging.% exists', t, t;
        END IF;
        IF source_table IS NOT NULL THEN
            EXECUTE format('LOCK TABLE conv.%I IN ACCESS EXCLUSIVE MODE', t);
        ELSE
            EXECUTE format('LOCK TABLE messaging.%I IN ACCESS EXCLUSIVE MODE', t);
        END IF;
    END LOOP;

    FOREACH t IN ARRAY ARRAY['conversations', 'conv_members', 'conv_read_seqs', 'conv_settings', 'conv_bots'] LOOP
        IF to_regclass(format('conv.%I', t)) IS NOT NULL THEN
            EXECUTE format('ALTER TABLE conv.%I SET SCHEMA messaging', t);
        END IF;
    END LOOP;

    -- Deliberately no CASCADE: unexpected objects or unowned sequences in conv
    -- are evidence that the cutover is incomplete and must roll back, not vanish.
    DROP SCHEMA IF EXISTS conv;
END
$$;
