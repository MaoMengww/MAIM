-- P4: an offline, atomic schema move. Stop the old conversation service first.
-- ALTER TABLE moves its indexes, constraints and owned sequences without copying
-- rows or changing object identity. Ambiguous source/destination copies abort;
-- never pick a winner or silently discard one side of a conflict.
DO $$
DECLARE
    t TEXT;
    source_table REGCLASS;
    target_table REGCLASS;
BEGIN
    -- Share the runner's lock, including when this SQL is executed directly.
    PERFORM pg_advisory_xact_lock(4278605, 1);
    CREATE SCHEMA IF NOT EXISTS msg;

    FOREACH t IN ARRAY ARRAY['conversations', 'conv_members', 'conv_read_seqs', 'conv_settings', 'conv_bots'] LOOP
        source_table := to_regclass(format('conv.%I', t));
        target_table := to_regclass(format('msg.%I', t));
        IF source_table IS NOT NULL AND target_table IS NOT NULL THEN
            RAISE EXCEPTION 'P4 schema conflict: both conv.% and msg.% exist; reconcile them before migrating', t, t;
        END IF;
        IF source_table IS NULL AND target_table IS NULL THEN
            RAISE EXCEPTION 'P4 missing conversation table: neither conv.% nor msg.% exists', t, t;
        END IF;
        IF source_table IS NOT NULL THEN
            EXECUTE format('LOCK TABLE conv.%I IN ACCESS EXCLUSIVE MODE', t);
        ELSE
            EXECUTE format('LOCK TABLE msg.%I IN ACCESS EXCLUSIVE MODE', t);
        END IF;
    END LOOP;

    FOREACH t IN ARRAY ARRAY['conversations', 'conv_members', 'conv_read_seqs', 'conv_settings', 'conv_bots'] LOOP
        IF to_regclass(format('conv.%I', t)) IS NOT NULL THEN
            EXECUTE format('ALTER TABLE conv.%I SET SCHEMA msg', t);
        END IF;
    END LOOP;

    -- Deliberately no CASCADE: unexpected objects or unowned sequences in conv
    -- are evidence that the cutover is incomplete and must roll back, not vanish.
    DROP SCHEMA IF EXISTS conv;
END
$$;
