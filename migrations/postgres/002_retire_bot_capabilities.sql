-- Retire application contracts without destroying historical runtime data.
-- The archive is never read by AIM. Physical archive cleanup needs separate approval.
DO $retire_bot_capabilities$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'bot' AND table_name = 'bots' AND column_name = 'memory_model_id'
    ) THEN
        CREATE SCHEMA IF NOT EXISTS bot_retired;
        CREATE TABLE bot_retired.memory_configs AS
            SELECT id AS bot_id, memory_model_name, memory_model_id,
                   memory_use_platform_model, memory_api_key_encrypted, memory_limit,
                   memory_embedding_model_name, memory_embedding_model_id
            FROM bot.bots;
        ALTER TABLE bot_retired.memory_configs ADD PRIMARY KEY (bot_id);
        ALTER TABLE bot.bots
            DROP COLUMN memory_model_name,
            DROP COLUMN memory_model_id,
            DROP COLUMN memory_use_platform_model,
            DROP COLUMN memory_api_key_encrypted,
            DROP COLUMN memory_limit,
            DROP COLUMN memory_embedding_model_name,
            DROP COLUMN memory_embedding_model_id;
    END IF;
    IF to_regclass('bot.conv_summaries') IS NOT NULL THEN
        CREATE SCHEMA IF NOT EXISTS bot_retired;
        ALTER TABLE bot.conv_summaries SET SCHEMA bot_retired;
    END IF;
    IF to_regclass('bot.summary_todos') IS NOT NULL THEN
        CREATE SCHEMA IF NOT EXISTS bot_retired;
        ALTER TABLE bot.summary_todos SET SCHEMA bot_retired;
    END IF;
    INSERT INTO public.schema_migrations(version)
        VALUES ('002_retire_bot_capabilities') ON CONFLICT DO NOTHING;
END;
$retire_bot_capabilities$;
