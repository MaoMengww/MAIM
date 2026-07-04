ALTER TABLE bot.bots
    ADD COLUMN IF NOT EXISTS memory_embedding_model_name TEXT,
    ADD COLUMN IF NOT EXISTS memory_embedding_model_id BIGINT DEFAULT 0;
