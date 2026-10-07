-- UUID 身份合同的不兼容新基线：仅初始化新环境，不映射或删除旧数据。
-- 旧写入服务必须停止；开发存储重建由第 08 票显式协调执行。
-- 所有实体由所属 domain 生成 UUIDv7；数据库没有实体发号默认值/sequence。
-- DO 保证 Docker init、psql 与启动 runner 都以一条原子语句应用全基线。
-- 已记录的新基线可以重复应用，旧迁移记录或旧实体类型必须显式失败。
DO $uuid_identity$
DECLARE
    has_baseline BOOLEAN;
    invalid_column TEXT;
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    CREATE TABLE IF NOT EXISTS public.schema_migrations (
        version VARCHAR(255) PRIMARY KEY,
        applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );

    SELECT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '000_uuid_identity')
      INTO has_baseline;
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version IN (
        '000_schema', '001_seq_unique', '003_bot_max_context_tokens', '004_drop_bot_memories',
        '005_bot_memory_embedding_model', '007_drop_audit', '008_merge_friend_into_user',
        '009_merge_conversation_into_message', '010_bot_runtime_memory_sequence',
        '011_knowledge_ingest_chunk_sequence', '012_bot_mcp_tool_sequence',
        '013_rename_schemas', '014_inbox_stream', '015_system_conversation',
        '016_inbox_retention', '017_inbox_changes', '018_personal_message_deletions'
    )) THEN
        RAISE EXCEPTION '旧身份迁移记录不支持 UUID 就地升级；请使用隔离的新 AIM 数据库，开发存储重建由第 08 票协调';
    END IF;
    IF NOT has_baseline AND (
        EXISTS (SELECT 1 FROM public.schema_migrations)
        OR EXISTS (
            SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname IN ('user', 'friend', 'conv', 'msg', 'notify', 'messaging', 'bot', 'knowledge', 'llm', 'file', 'realtime', 'audit')
              AND c.relkind IN ('r', 'p', 'S')
        )
        OR EXISTS (
            SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'public'
              AND c.relname IN ('users', 'user_devices', 'friends', 'friend_groups', 'friend_requests', 'user_blocks', 'model_registry_id_seq')
        )
    ) THEN
        RAISE EXCEPTION '现有 AIM 存储不是已初始化的 UUID 基线；拒绝自动清空、搬移或映射旧数据';
    END IF;
	IF EXISTS (
		SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE (n.nspname IN ('user', 'messaging', 'bot', 'knowledge', 'llm', 'file', 'realtime') AND c.relkind = 'S')
		   OR (n.nspname IN ('friend', 'conv', 'msg', 'notify', 'audit') AND c.relkind IN ('r', 'p', 'S'))
	) THEN
		RAISE EXCEPTION 'UUID 基线不允许旧域副本或实体发号 sequence；拒绝删除或转换这些对象';
	END IF;

    SELECT format('%I.%I.%I', expected.schema_name, expected.table_name, field.column_name)
      INTO invalid_column
      FROM (VALUES
            ('user', 'users', ARRAY['id']),
            ('user', 'user_devices', ARRAY['id', 'user_id']),
            ('user', 'user_blocks', ARRAY['id', 'user_id', 'blocked_user_id']),
            ('user', 'friends', ARRAY['id', 'user_id', 'friend_id', 'group_id']),
            ('user', 'friend_groups', ARRAY['id', 'user_id']),
            ('user', 'friend_requests', ARRAY['id', 'from_user_id', 'to_user_id']),
            ('messaging', 'messages', ARRAY['id', 'conv_id', 'sender_id', 'client_msg_id', 'reply_to_msg_id']),
            ('messaging', 'broadcasts', ARRAY['id', 'sender_id', 'scope_target_id']),
            ('messaging', 'sequences', ARRAY['conv_id']),
            ('messaging', 'failed_events', ARRAY['id']),
            ('messaging', 'outbox_events', ARRAY['id', 'conv_id']),
            ('messaging', 'conversations', ARRAY['id', 'owner_id', 'last_message_id']),
            ('messaging', 'conv_members', ARRAY['id', 'conv_id', 'user_id', 'bot_id']),
            ('messaging', 'conv_read_seqs', ARRAY['id', 'conv_id', 'user_id']),
            ('messaging', 'conv_settings', ARRAY['id', 'conv_id', 'user_id']),
            ('messaging', 'conv_bots', ARRAY['id', 'conv_id', 'bot_id', 'added_by']),
            ('bot', 'bots', ARRAY['id', 'owner_id', 'pseudo_user_id', 'model_id', 'memory_model_id', 'memory_embedding_model_id']),
            ('bot', 'mcp_servers', ARRAY['id', 'owner_id', 'created_by']),
            ('bot', 'bot_mcp_servers', ARRAY['id', 'bot_id', 'mcp_server_id']),
            ('bot', 'mcp_tools', ARRAY['id', 'mcp_server_id']),
            ('bot', 'conv_summaries', ARRAY['id', 'conv_id', 'user_id']),
            ('bot', 'summary_todos', ARRAY['id', 'summary_id', 'conv_id']),
            ('file', 'files', ARRAY['id', 'uploader_id']),
            ('knowledge', 'knowledge_bases', ARRAY['id', 'owner_id', 'embedding_model_id']),
            ('knowledge', 'documents', ARRAY['id', 'kb_id']),
            ('knowledge', 'document_chunks', ARRAY['id', 'doc_id', 'kb_id', 'parent_chunk_id']),
            ('knowledge', 'knowledge_bindings', ARRAY['id', 'kb_id', 'target_id']),
            ('llm', 'model_registry', ARRAY['id', 'owner_id']),
            ('llm', 'billing_records', ARRAY['id', 'bot_id', 'owner_id', 'model_id']),
            ('realtime', 'notifications', ARRAY['id', 'user_id', 'reference_id']),
            ('realtime', 'device_tokens', ARRAY['id', 'user_id']),
            ('messaging', 'inbox_streams', ARRAY['user_id']),
            ('messaging', 'inbox_entries', ARRAY['user_id', 'conv_id', 'message_id', 'change_id']),
            ('messaging', 'inbox_applied_changes', ARRAY['user_id', 'change_id']),
            ('messaging', 'personal_message_deletions', ARRAY['user_id', 'conv_id', 'message_id'])
      ) AS expected(schema_name, table_name, columns)
      CROSS JOIN LATERAL unnest(expected.columns) AS field(column_name)
      JOIN pg_namespace n ON n.nspname = expected.schema_name
      JOIN pg_class c ON c.relnamespace = n.oid AND c.relname = expected.table_name
      JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = field.column_name AND NOT a.attisdropped
      WHERE a.atttypid <> 'uuid'::regtype
      LIMIT 1;
    IF invalid_column IS NOT NULL THEN
        RAISE EXCEPTION '实体身份字段 % 必须使用 uuid；不支持旧数值身份映射', invalid_column;
    END IF;
    IF has_baseline THEN
        RETURN;
    END IF;

-- =========== Schemas ===========
CREATE SCHEMA IF NOT EXISTS bot;
CREATE SCHEMA IF NOT EXISTS file;
CREATE SCHEMA IF NOT EXISTS knowledge;
CREATE SCHEMA IF NOT EXISTS llm;
CREATE SCHEMA IF NOT EXISTS messaging;
CREATE SCHEMA IF NOT EXISTS realtime;
CREATE SCHEMA IF NOT EXISTS "user";

-- =========== user domain (accounts and relationships) ===========

CREATE TABLE IF NOT EXISTS "user".users (
    id            UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    username      VARCHAR(64),
    password_hash VARCHAR(256),
    phone         VARCHAR(20),
    email         VARCHAR(128),
    avatar        VARCHAR(512),
    gender        SMALLINT,
    bio           TEXT,
    birthday      BIGINT,
    balance       NUMERIC(12,6) DEFAULT 0,
    settings      JSONB DEFAULT '{}'::JSONB,
    created_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS "user".user_devices (
    id              UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id         UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    device_id       VARCHAR(128),
    platform        VARCHAR(32) DEFAULT 'web',
    push_token      VARCHAR(512),
    ip              VARCHAR(64),
    location        VARCHAR(128),
    last_active_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS "user".user_blocks (
    id              UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id         UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    blocked_user_id UUID NOT NULL CHECK (blocked_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON "user".users(username);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_phone   ON "user".users(phone) WHERE (phone::TEXT <> ''::TEXT);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email   ON "user".users(email) WHERE (email::TEXT <> ''::TEXT);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_unique ON "user".user_devices(user_id, device_id);
CREATE INDEX IF NOT EXISTS idx_user_devices_user ON "user".user_devices(user_id);
CREATE INDEX IF NOT EXISTS idx_user_devices_user_id ON "user".user_devices(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_blocks_pair ON "user".user_blocks(user_id, blocked_user_id);
CREATE INDEX IF NOT EXISTS idx_user_blocks_user ON "user".user_blocks(user_id);

-- =========== messaging domain ===========

-- submission_content 是规范化原始发送依据，不随后续消息编辑变化。
-- 内部系统消息和 Bot 回复没有客户端提交动作，提交键/原始依据均为 NULL。
CREATE TABLE IF NOT EXISTS messaging.messages (
    id              UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id         UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    sender_id       UUID CHECK (sender_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    sender_type     TEXT DEFAULT 'user',
    client_msg_id   UUID CHECK (client_msg_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    seq             BIGINT NOT NULL CHECK (seq BETWEEN 1 AND 9007199254740991),
    msg_type        INTEGER,
    content         JSONB,
    reply_to_msg_id UUID CHECK (reply_to_msg_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    status          SMALLINT DEFAULT 1 NOT NULL,
    edit_history    JSONB,
    edit_count      INTEGER,
    created_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ,
    submission_content JSONB,
    CONSTRAINT messages_submission_key_v4 CHECK (client_msg_id IS NULL OR client_msg_id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT messages_submission_content CHECK ((client_msg_id IS NULL AND submission_content IS NULL) OR (client_msg_id IS NOT NULL AND sender_id IS NOT NULL AND submission_content IS NOT NULL))
);


CREATE TABLE IF NOT EXISTS messaging.broadcasts (
    id              UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    sender_id       UUID NOT NULL CHECK (sender_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    content         JSONB DEFAULT '{}'::JSONB NOT NULL,
    scope           VARCHAR(32) DEFAULT 'all' NOT NULL,
    scope_target_id UUID CHECK (scope_target_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    created_at      TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    CONSTRAINT broadcasts_scope_target CHECK ((scope = 'all' AND scope_target_id IS NULL) OR (scope IN ('group', 'user') AND scope_target_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS messaging.sequences (
    conv_id     UUID PRIMARY KEY CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    current_seq BIGINT NOT NULL DEFAULT 0 CHECK (current_seq BETWEEN 0 AND 9007199254740991)
);

CREATE TABLE IF NOT EXISTS messaging.failed_events (
    id          UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    topic       VARCHAR(64) NOT NULL,
    key         VARCHAR(64) NOT NULL,
    payload     JSONB NOT NULL,
    retry_count BIGINT DEFAULT 0,
    last_error  VARCHAR(256),
    created_at  TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS messaging.outbox_events (
    id            UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    topic         VARCHAR(64) NOT NULL,
    key           VARCHAR(128) NOT NULL,
    payload       JSONB NOT NULL,
    status        SMALLINT DEFAULT 0 NOT NULL,
    retry_count   INTEGER DEFAULT 0 NOT NULL,
    max_retries   INTEGER DEFAULT 10 NOT NULL,
    next_retry_at TIMESTAMPTZ,
    last_error    TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    dispatched_at TIMESTAMPTZ,
    conv_id UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    publication_sequence BIGINT NOT NULL CHECK (publication_sequence BETWEEN 1 AND 9007199254740991),
    CONSTRAINT outbox_events_stream_sequence UNIQUE (conv_id, publication_sequence)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_conv_seq ON messaging.messages(conv_id, seq);
CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_submission ON messaging.messages(sender_id, conv_id, client_msg_id) WHERE client_msg_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_sender ON messaging.messages(sender_id);
CREATE INDEX IF NOT EXISTS idx_messages_created ON messaging.messages(created_at);
CREATE INDEX IF NOT EXISTS idx_conv_seq ON messaging.messages(conv_id);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON messaging.outbox_events(status, next_retry_at, created_at);
CREATE INDEX IF NOT EXISTS idx_outbox_stream_pending ON messaging.outbox_events(conv_id, publication_sequence) WHERE status <> 1;

-- =========== messaging domain (conversations and membership) ===========


CREATE TABLE IF NOT EXISTS messaging.conversations (
    id                   UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    type                 INTEGER,
    name                 TEXT,
    avatar               TEXT,
    owner_id             UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    announcement         TEXT,
    is_muted_all         BOOLEAN,
    background           TEXT,
    max_seq              BIGINT NOT NULL DEFAULT 0 CHECK (max_seq BETWEEN 0 AND 9007199254740991),
    last_message_id      UUID CHECK (last_message_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    last_message_preview TEXT,
    member_count         INTEGER,
    created_at           TIMESTAMPTZ,
    updated_at           TIMESTAMPTZ,
    publication_sequence BIGINT NOT NULL DEFAULT 0 CHECK (publication_sequence BETWEEN 0 AND 9007199254740991)
);

CREATE TABLE IF NOT EXISTS messaging.conv_members (
    id          UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id     UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id     UUID CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    member_type TEXT NOT NULL,
    bot_id      UUID CHECK (bot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    role        INTEGER,
    alias       TEXT,
    is_muted    BOOLEAN,
    mute_until  BIGINT,
    joined_at   TIMESTAMPTZ,
    CONSTRAINT conv_members_identity CHECK ((member_type = 'user' AND user_id IS NOT NULL AND bot_id IS NULL) OR (member_type = 'bot' AND bot_id IS NOT NULL AND user_id IS NULL))
);

CREATE TABLE IF NOT EXISTS messaging.conv_read_seqs (
    id             UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id        UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id        UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    last_read_seq  BIGINT NOT NULL DEFAULT 0 CHECK (last_read_seq BETWEEN 0 AND 9007199254740991),
    read_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS messaging.conv_settings (
    id        UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id   UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id   UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    is_muted  BOOLEAN,
    is_pinned BOOLEAN
);

CREATE TABLE IF NOT EXISTS messaging.conv_bots (
    id                UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id           UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    bot_id            UUID NOT NULL CHECK (bot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    added_by          UUID NOT NULL CHECK (added_by <> '00000000-0000-0000-0000-000000000000'::uuid),
    response_triggers JSONB,
    bot_settings      TEXT,
    created_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_conv_members_conv ON messaging.conv_members(conv_id);
CREATE INDEX IF NOT EXISTS idx_conv_members_user ON messaging.conv_members(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_members_pair ON messaging.conv_members(conv_id, user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_members_bot_pair ON messaging.conv_members(conv_id, bot_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_read_seqs_pair ON messaging.conv_read_seqs(conv_id, user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_settings_pair ON messaging.conv_settings(conv_id, user_id);
CREATE INDEX IF NOT EXISTS idx_conv_bots_conv ON messaging.conv_bots(conv_id);
CREATE INDEX IF NOT EXISTS idx_conv_bots_bot ON messaging.conv_bots(bot_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_bots_pair ON messaging.conv_bots(conv_id, bot_id);

-- =========== bot domain ===========

CREATE TABLE IF NOT EXISTS bot.bots (
    id                       UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    owner_id                 UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name                     TEXT,
    avatar                   TEXT,
    type                     TEXT,
    pseudo_user_id           UUID CHECK (pseudo_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    status                   TEXT DEFAULT 'active',
    use_platform_model       BOOLEAN DEFAULT TRUE NOT NULL,
    model_name               TEXT,
    model_id                 UUID CHECK (model_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    base_url                 TEXT,
    api_key_encrypted        TEXT,
    system_prompt            TEXT,
    persona                  TEXT,
    enable_knowledge         BOOLEAN DEFAULT TRUE NOT NULL,
    temperature              DOUBLE PRECISION DEFAULT 0.7 NOT NULL,
    max_context_messages     INTEGER DEFAULT 10 NOT NULL,
    max_context_tokens       INTEGER DEFAULT 0 NOT NULL,
    streaming_enabled        BOOLEAN DEFAULT TRUE NOT NULL,
    memory_model_name        TEXT,
    memory_use_platform_model BOOLEAN DEFAULT TRUE NOT NULL,
    memory_api_key_encrypted TEXT,
    conn_mode                TEXT,
    webhook_secret           TEXT,
    callback_url             TEXT,
    app_secret_hash          TEXT,
    bot_tags                 JSONB,
    capabilities             JSONB,
    settings                 JSONB,
    created_at               TIMESTAMPTZ,
    updated_at               TIMESTAMPTZ,
    template_id              TEXT,
    sub_type                 TEXT,
    memory_model_id          UUID CHECK (memory_model_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    memory_limit             INTEGER DEFAULT 0 NOT NULL,
    memory_embedding_model_name TEXT,
    memory_embedding_model_id UUID CHECK (memory_embedding_model_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    response_triggers        JSONB,
    max_step INTEGER NOT NULL DEFAULT 5,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('platform', 'user')),
    CONSTRAINT bots_ownership CHECK ((owner_type = 'platform' AND owner_id IS NULL) OR (owner_type = 'user' AND owner_id IS NOT NULL))
);


CREATE TABLE IF NOT EXISTS bot.mcp_servers (
    id              UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name            TEXT,
    description     TEXT,
    transport       TEXT DEFAULT 'sse',
    url             TEXT,
    command         TEXT,
    args            TEXT[],
    env             JSONB,
    discovery       VARCHAR(32) DEFAULT 'dynamic' NOT NULL,
    tools           TEXT[] DEFAULT '{}'::TEXT[] NOT NULL,
    timeout_ms      INTEGER DEFAULT 10000 NOT NULL,
    status          VARCHAR(32) DEFAULT 'active' NOT NULL,
    auth_config     JSONB,
    advanced_config JSONB,
    enabled         BOOLEAN DEFAULT TRUE NOT NULL,
    created_by      UUID CHECK (created_by <> '00000000-0000-0000-0000-000000000000'::uuid),
    created_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('platform', 'user')),
    owner_id UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT mcp_servers_ownership CHECK ((owner_type = 'platform' AND owner_id IS NULL) OR (owner_type = 'user' AND owner_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS bot.bot_mcp_servers (
    id              UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    bot_id          UUID NOT NULL CHECK (bot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    mcp_server_id   UUID NOT NULL CHECK (mcp_server_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    enabled         BOOLEAN DEFAULT TRUE NOT NULL,
    config_override JSONB,
    created_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS bot.mcp_tools (
    id            UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    mcp_server_id UUID NOT NULL CHECK (mcp_server_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name          TEXT,
    description   TEXT,
    input_schema  TEXT,
    created_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS bot.conv_summaries (
    id            UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id       UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id       UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    range_type    VARCHAR(20) NOT NULL,
    range_start   BIGINT,
    range_end     BIGINT,
    message_count INTEGER DEFAULT 0 NOT NULL,
    summary       TEXT NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW() NOT NULL
);

CREATE TABLE IF NOT EXISTS bot.summary_todos (
    id          UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    summary_id  UUID REFERENCES bot.conv_summaries(id) ON DELETE CASCADE CHECK (summary_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id     UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    content     TEXT NOT NULL,
    done        BOOLEAN DEFAULT FALSE NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    updated_at  TIMESTAMPTZ DEFAULT NOW() NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_bots_owner ON bot.bots(owner_id);
CREATE INDEX IF NOT EXISTS idx_bots_status ON bot.bots(status);
CREATE INDEX IF NOT EXISTS idx_bots_type ON bot.bots(type);
CREATE INDEX IF NOT EXISTS idx_bots_model_id ON bot.bots(model_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bots_pseudo_user ON bot.bots(pseudo_user_id);
CREATE UNIQUE INDEX IF NOT EXISTS uni_bots_pseudo_user_id ON bot.bots(pseudo_user_id);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_status ON bot.mcp_servers(status);
CREATE INDEX IF NOT EXISTS idx_bot_mcp_servers_bot ON bot.bot_mcp_servers(bot_id);
CREATE INDEX IF NOT EXISTS idx_bot_mcp_servers_mcp ON bot.bot_mcp_servers(mcp_server_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bot_mcp_servers_pair ON bot.bot_mcp_servers(bot_id, mcp_server_id);
CREATE INDEX IF NOT EXISTS idx_mcp_tools_mcp_server_id ON bot.mcp_tools(mcp_server_id);
CREATE INDEX IF NOT EXISTS idx_conv_summaries_conv ON bot.conv_summaries(conv_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_summary_todos_conv ON bot.summary_todos(conv_id);
CREATE INDEX IF NOT EXISTS idx_summary_todos_summary ON bot.summary_todos(summary_id);


-- =========== user domain (relationships) ===========

CREATE TABLE IF NOT EXISTS "user".friends (
    id         UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id    UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    friend_id  UUID NOT NULL CHECK (friend_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    group_id   UUID CHECK (group_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    remark     VARCHAR(64) DEFAULT '' NOT NULL,
    created_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS "user".friend_groups (
    id         UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id    UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name       VARCHAR(64) NOT NULL,
    sort_order INTEGER DEFAULT 0 NOT NULL,
    created_at TIMESTAMPTZ
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'friends_group_reference' AND conrelid = '"user".friends'::regclass) THEN
        ALTER TABLE "user".friends ADD CONSTRAINT friends_group_reference
            FOREIGN KEY (group_id) REFERENCES "user".friend_groups(id) ON DELETE SET NULL;
    END IF;
END;
$$;

CREATE TABLE IF NOT EXISTS "user".friend_requests (
    id           UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    from_user_id UUID NOT NULL CHECK (from_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    to_user_id   UUID NOT NULL CHECK (to_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    message      VARCHAR(256) DEFAULT '' NOT NULL,
    status       SMALLINT DEFAULT 0 NOT NULL,
    created_at   TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_friends_user ON "user".friends(user_id);
CREATE INDEX IF NOT EXISTS idx_friends_friend ON "user".friends(friend_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_friends_pair ON "user".friends(user_id, friend_id);
CREATE INDEX IF NOT EXISTS idx_friend_groups_user ON "user".friend_groups(user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_from ON "user".friend_requests(from_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_to ON "user".friend_requests(to_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_status ON "user".friend_requests(status);

-- =========== file domain ===========

CREATE TABLE IF NOT EXISTS file.files (
    id          UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name        VARCHAR(512) DEFAULT '' NOT NULL,
    key         VARCHAR(512) NOT NULL,
    size        BIGINT DEFAULT 0 NOT NULL,
    mime_type   VARCHAR(256) DEFAULT '' NOT NULL,
    ext         VARCHAR(32) DEFAULT '' NOT NULL,
    width       INTEGER DEFAULT 0 NOT NULL,
    height      INTEGER DEFAULT 0 NOT NULL,
    duration    INTEGER DEFAULT 0 NOT NULL,
    md5         VARCHAR(64) DEFAULT '' NOT NULL,
    purpose     SMALLINT DEFAULT 0 NOT NULL,
    access      SMALLINT DEFAULT 0 NOT NULL,
    uploader_id UUID NOT NULL CHECK (uploader_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    bucket      VARCHAR(128) DEFAULT 'aim' NOT NULL,
    created_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_files_uploader ON file.files(uploader_id);
CREATE INDEX IF NOT EXISTS idx_files_key ON file.files(key);

-- =========== knowledge domain ===========

CREATE TABLE IF NOT EXISTS knowledge.knowledge_bases (
    id                  UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    owner_id            UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name                TEXT,
    description         TEXT,
    embedding_model     TEXT,
    pipeline_config     JSONB,
    doc_count           BIGINT,
    total_chunks        BIGINT,
    status              TEXT,
    created_at          TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ,
    mode                VARCHAR(16) DEFAULT 'rag' NOT NULL,
    embedding_model_id  UUID CHECK (embedding_model_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    last_maintenance_at TIMESTAMPTZ,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('platform', 'user')),
    CONSTRAINT knowledge_bases_ownership CHECK ((owner_type = 'platform' AND owner_id IS NULL) OR (owner_type = 'user' AND owner_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS knowledge.documents (
    id                UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    kb_id             UUID NOT NULL CHECK (kb_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    title             TEXT,
    file_type         TEXT,
    file_size         BIGINT,
    original_filename TEXT,
    minio_bucket      TEXT,
    minio_key         TEXT,
    content_hash      TEXT,
    status            TEXT,
    chunk_count       BIGINT,
    error_message     TEXT,
    pipeline_override JSONB,
    metadata          JSONB,
    stages            JSONB DEFAULT '[]'::JSONB NOT NULL,
    created_at        TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS knowledge.document_chunks (
    id              UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    doc_id          UUID NOT NULL CHECK (doc_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    kb_id           UUID NOT NULL CHECK (kb_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    chunk_index     BIGINT,
    content         TEXT,
    token_count     BIGINT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ,
    parent_chunk_id UUID CHECK (parent_chunk_id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

CREATE TABLE IF NOT EXISTS knowledge.knowledge_bindings (
    id          UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    kb_id       UUID NOT NULL CHECK (kb_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    target_type TEXT,
    target_id   UUID NOT NULL CHECK (target_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    created_at  TIMESTAMPTZ,
    kb_name     TEXT
);

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_owner ON knowledge.knowledge_bases(owner_id);
CREATE INDEX IF NOT EXISTS idx_documents_kb ON knowledge.documents(kb_id);
CREATE INDEX IF NOT EXISTS idx_documents_status ON knowledge.documents(status);
CREATE INDEX IF NOT EXISTS idx_document_chunks_doc ON knowledge.document_chunks(doc_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_kb ON knowledge.document_chunks(kb_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_parent_chunk_id ON knowledge.document_chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bindings_kb ON knowledge.knowledge_bindings(kb_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bindings_target ON knowledge.knowledge_bindings(target_type, target_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_bindings_pair ON knowledge.knowledge_bindings(kb_id, target_type, target_id);

-- =========== llm domain ===========

CREATE TABLE IF NOT EXISTS llm.model_registry (
    id                    UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    model_name            TEXT,
    provider              TEXT,
    capability            TEXT,
    base_url              TEXT,
    api_key_encrypted     TEXT,
    context_window        BIGINT,
    max_output_tokens     BIGINT,
    input_price_per_mtok  DOUBLE PRECISION,
    output_price_per_mtok DOUBLE PRECISION,
    status                TEXT,
    owner_id              UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    metadata              TEXT,
    created_at            TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('platform', 'user')),
    CONSTRAINT model_registry_ownership CHECK ((owner_type = 'platform' AND owner_id IS NULL) OR (owner_type = 'user' AND owner_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS llm.billing_records (
    id            UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    bot_id        UUID CHECK (bot_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    owner_id      UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    model_name    TEXT,
    capability    TEXT,
    input_tokens  BIGINT,
    output_tokens BIGINT,
    input_cost    NUMERIC,
    output_cost   NUMERIC,
    provider      TEXT,
    created_at    TIMESTAMPTZ,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('platform', 'user')),
    model_id UUID CHECK (model_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT billing_records_ownership CHECK ((owner_type = 'platform' AND owner_id IS NULL) OR (owner_type = 'user' AND owner_id IS NOT NULL))
);


CREATE INDEX IF NOT EXISTS idx_model_registry_provider ON llm.model_registry(provider);
CREATE INDEX IF NOT EXISTS idx_model_registry_status ON llm.model_registry(status);
CREATE INDEX IF NOT EXISTS idx_model_registry_owner ON llm.model_registry(owner_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_bot ON llm.billing_records(bot_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_owner ON llm.billing_records(owner_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_time ON llm.billing_records(created_at);
CREATE INDEX IF NOT EXISTS idx_billing_records_model ON llm.billing_records(model_id);

-- =========== realtime domain ===========

-- reference_id 仅关联 AIM 本地实体；对象种类和引用必须一同出现。
-- device_id、推送 token/provider 等专用标识保持原文本合同。
CREATE TABLE IF NOT EXISTS realtime.notifications (
    id           UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id      UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    type         INTEGER,
    title        TEXT,
    content      TEXT,
    is_read      BOOLEAN,
    reference_id UUID CHECK (reference_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    created_at   BIGINT,
    reference_type TEXT,
    CONSTRAINT notifications_reference CHECK ((reference_id IS NULL AND reference_type IS NULL) OR (reference_id IS NOT NULL AND reference_type IS NOT NULL AND reference_type <> ''))
);

CREATE TABLE IF NOT EXISTS realtime.device_tokens (
    id        UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    user_id   UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    device_id VARCHAR(128),
    platform  VARCHAR(16),
    token     VARCHAR(512),
    provider  VARCHAR(8),
    created_at BIGINT,
    updated_at BIGINT
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON realtime.notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read ON realtime.notifications(user_id, is_read);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_device ON realtime.device_tokens(user_id, device_id);

-- =========== messaging domain (committed user sync stream) ===========
-- Stream allocation and entry insertion share a transaction and row lock. The
-- end and retained prefix never reset when entries are collected.
CREATE TABLE IF NOT EXISTS messaging.inbox_streams (
    user_id UUID PRIMARY KEY CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    position BIGINT NOT NULL DEFAULT 0 CHECK (position BETWEEN 0 AND 9007199254740991),
    retained_position BIGINT NOT NULL DEFAULT 0 CHECK (retained_position BETWEEN 0 AND position)
);

-- Conversation/message references deliberately have no foreign key: removal and
-- tombstone changes must replay after the referenced business row disappears.
CREATE TABLE IF NOT EXISTS messaging.inbox_entries (
    user_id UUID NOT NULL REFERENCES messaging.inbox_streams(user_id) ON DELETE CASCADE CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    position BIGINT NOT NULL CHECK (position BETWEEN 1 AND 9007199254740991),
    conv_id UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    message_id UUID CHECK (message_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    kind TEXT NOT NULL CHECK (kind <> ''),
    change_id UUID NOT NULL CHECK (change_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    last_read_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_read_seq BETWEEN 0 AND 9007199254740991),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, position),
    CONSTRAINT inbox_entries_message_reference CHECK (kind NOT IN ('message.new', 'message.edited', 'message.recalled', 'message.deleted') OR message_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_inbox_entries_created ON messaging.inbox_entries(created_at);
CREATE INDEX IF NOT EXISTS idx_inbox_entries_conv ON messaging.inbox_entries(user_id, conv_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_inbox_change_id ON messaging.inbox_entries(user_id, change_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_inbox_own_read ON messaging.inbox_entries(user_id, conv_id, kind) WHERE kind = 'read.updated';

-- Replay deduplication and allocated checkpoint provenance outlive coalescing and retention.
CREATE TABLE IF NOT EXISTS messaging.inbox_applied_changes (
    user_id UUID NOT NULL REFERENCES messaging.inbox_streams(user_id) ON DELETE CASCADE CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    change_id UUID NOT NULL CHECK (change_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    position BIGINT CHECK (position BETWEEN 1 AND 9007199254740991),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, change_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_inbox_applied_position ON messaging.inbox_applied_changes(user_id, position) WHERE position IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_inbox_applied_created ON messaging.inbox_applied_changes(created_at) WHERE position IS NOT NULL;

-- Account deletion state survives inbox collection and physical message deletion.
CREATE TABLE IF NOT EXISTS messaging.personal_message_deletions (
    user_id UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    conv_id UUID NOT NULL CHECK (conv_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    message_id UUID NOT NULL CHECK (message_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, conv_id, message_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_conversations_system_owner
    ON messaging.conversations(owner_id) WHERE type = 3;

-- 与 RunMigrations 共用同一版本，避免 Compose 初始化后再重复登记/重放。
INSERT INTO public.schema_migrations(version) VALUES ('000_uuid_identity') ON CONFLICT DO NOTHING;
END;
$uuid_identity$;
