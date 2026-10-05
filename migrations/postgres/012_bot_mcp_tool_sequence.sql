-- Discovery persists tools without process-local IDs; existing databases need
-- the same sequence default as a fresh bootstrap, without reusing stored IDs.
CREATE SEQUENCE IF NOT EXISTS bot.mcp_tools_id_seq;
ALTER SEQUENCE bot.mcp_tools_id_seq OWNED BY bot.mcp_tools.id;
SELECT setval(
    'bot.mcp_tools_id_seq',
    GREATEST(
        COALESCE((SELECT MAX(id) FROM bot.mcp_tools), 1),
        (SELECT last_value FROM bot.mcp_tools_id_seq)
    ),
    (SELECT is_called FROM bot.mcp_tools_id_seq) OR EXISTS (SELECT 1 FROM bot.mcp_tools)
);
ALTER TABLE bot.mcp_tools ALTER COLUMN id SET DEFAULT nextval('bot.mcp_tools_id_seq');
