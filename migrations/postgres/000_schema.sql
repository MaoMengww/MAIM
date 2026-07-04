-- AIM Database Schema (synced from real DB 2026-06-10)
-- All statements use IF NOT EXISTS / IF EXISTS — safe to re-run.
-- For new migrations, add sequentially numbered files (001_xxx.sql, 002_xxx.sql, …).

-- =========== Schemas ===========
CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS bot;
CREATE SCHEMA IF NOT EXISTS conv;
CREATE SCHEMA IF NOT EXISTS file;
CREATE SCHEMA IF NOT EXISTS friend;
CREATE SCHEMA IF NOT EXISTS knowledge;
CREATE SCHEMA IF NOT EXISTS llm;
CREATE SCHEMA IF NOT EXISTS msg;
CREATE SCHEMA IF NOT EXISTS notify;
CREATE SCHEMA IF NOT EXISTS "user";

-- =========== Migration tracking ===========
CREATE TABLE IF NOT EXISTS public.schema_migrations (
    version    VARCHAR(16) PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- =========== public (search_path default) ===========
-- user-service / friend-service AutoMigrate writes here because models
-- have no schema-qualified TableName().

CREATE TABLE IF NOT EXISTS public.users (
    id            BIGINT PRIMARY KEY,
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

CREATE TABLE IF NOT EXISTS public.user_devices (
    id              BIGINT PRIMARY KEY DEFAULT nextval('public.user_devices_id_seq'::regclass),
    user_id         BIGINT,
    device_id       VARCHAR(128),
    platform        VARCHAR(32) DEFAULT 'web',
    push_token      VARCHAR(512),
    ip              VARCHAR(64),
    location        VARCHAR(128),
    last_active_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS public.user_devices_id_seq;
ALTER SEQUENCE public.user_devices_id_seq OWNED BY public.user_devices.id;

CREATE SEQUENCE IF NOT EXISTS public.users_id_seq;
ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;

CREATE INDEX IF NOT EXISTS idx_user_devices_user_id ON public.user_devices(user_id);

-- =========== "user" domain (from old SQL migration, partially overlapping with public) ===========

CREATE TABLE IF NOT EXISTS "user".users (
    id            BIGINT PRIMARY KEY,
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
    id              BIGINT PRIMARY KEY,
    user_id         BIGINT,
    device_id       VARCHAR(128),
    platform        VARCHAR(32) DEFAULT 'web',
    push_token      VARCHAR(512),
    ip              VARCHAR(64),
    location        VARCHAR(128),
    last_active_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS "user".user_devices_id_seq;
ALTER SEQUENCE "user".user_devices_id_seq OWNED BY "user".user_devices.id;

CREATE TABLE IF NOT EXISTS "user".user_blocks (
    id              BIGINT PRIMARY KEY,
    user_id         BIGINT NOT NULL,
    blocked_user_id BIGINT NOT NULL,
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

-- =========== msg domain ===========

CREATE TABLE IF NOT EXISTS msg.messages (
    id              BIGINT PRIMARY KEY,
    conv_id         BIGINT,
    sender_id       BIGINT,
    sender_type     TEXT DEFAULT 'user',
    client_msg_id   TEXT,
    seq             BIGINT,
    msg_type        INTEGER,
    content         JSONB,
    reply_to_msg_id BIGINT,
    status          SMALLINT DEFAULT 1 NOT NULL,
    edit_history    JSONB,
    edit_count      INTEGER,
    created_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS msg.user_inbox (
    user_id       BIGINT NOT NULL,
    conv_id       BIGINT NOT NULL,
    message_id    BIGINT NOT NULL,
    seq           BIGINT NOT NULL,
    last_read_seq BIGINT DEFAULT 0 NOT NULL,
    is_deleted    BOOLEAN DEFAULT FALSE NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    PRIMARY KEY (user_id, conv_id, seq)
);

CREATE TABLE IF NOT EXISTS msg.broadcasts (
    id              BIGINT PRIMARY KEY,
    sender_id       BIGINT NOT NULL,
    content         JSONB DEFAULT '{}'::JSONB NOT NULL,
    scope           VARCHAR(32) DEFAULT 'all' NOT NULL,
    scope_target_id BIGINT DEFAULT 0 NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW() NOT NULL
);

CREATE TABLE IF NOT EXISTS msg.sequences (
    conv_id     BIGINT PRIMARY KEY,
    current_seq BIGINT DEFAULT 0 NOT NULL
);

CREATE TABLE IF NOT EXISTS msg.failed_events (
    id          BIGINT PRIMARY KEY,
    topic       VARCHAR(64) NOT NULL,
    key         VARCHAR(64) NOT NULL,
    payload     JSONB NOT NULL,
    retry_count BIGINT DEFAULT 0,
    last_error  VARCHAR(256),
    created_at  TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS msg.failed_events_id_seq;
ALTER SEQUENCE msg.failed_events_id_seq OWNED BY msg.failed_events.id;

CREATE TABLE IF NOT EXISTS msg.outbox_events (
    id            BIGINT PRIMARY KEY,
    topic         VARCHAR(64) NOT NULL,
    key           VARCHAR(128) NOT NULL,
    payload       JSONB NOT NULL,
    status        SMALLINT DEFAULT 0 NOT NULL,
    retry_count   INTEGER DEFAULT 0 NOT NULL,
    max_retries   INTEGER DEFAULT 10 NOT NULL,
    next_retry_at TIMESTAMPTZ,
    last_error    TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    dispatched_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_messages_conv_seq ON msg.messages(conv_id, seq);
CREATE INDEX IF NOT EXISTS idx_messages_sender ON msg.messages(sender_id);
CREATE INDEX IF NOT EXISTS idx_messages_created ON msg.messages(created_at);
CREATE INDEX IF NOT EXISTS idx_conv_seq ON msg.messages(conv_id);
CREATE INDEX IF NOT EXISTS idx_user_inbox_conv ON msg.user_inbox(user_id, conv_id);
CREATE INDEX IF NOT EXISTS idx_user_inbox_covering ON msg.user_inbox(user_id, conv_id, seq DESC) INCLUDE (message_id, last_read_seq, created_at) WHERE (is_deleted = FALSE);
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON msg.outbox_events(status, next_retry_at, created_at);

-- =========== conv domain ===========

CREATE TABLE IF NOT EXISTS conv.conversations (
    id                   BIGINT PRIMARY KEY,
    type                 INTEGER,
    name                 TEXT,
    avatar               TEXT,
    owner_id             BIGINT,
    announcement         TEXT,
    is_muted_all         BOOLEAN,
    background           TEXT,
    max_seq              BIGINT,
    last_message_id      BIGINT,
    last_message_preview TEXT,
    member_count         INTEGER,
    created_at           TIMESTAMPTZ,
    updated_at           TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS conv.conv_members (
    id          BIGINT PRIMARY KEY,
    conv_id     BIGINT,
    user_id     BIGINT,
    member_type TEXT,
    bot_id      BIGINT,
    role        INTEGER,
    alias       TEXT,
    is_muted    BOOLEAN,
    mute_until  BIGINT,
    joined_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS conv.conv_read_seqs (
    id             BIGINT PRIMARY KEY,
    conv_id        BIGINT,
    user_id        BIGINT,
    last_read_seq  BIGINT,
    read_at        TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS conv.conv_settings (
    id        BIGINT PRIMARY KEY,
    conv_id   BIGINT,
    user_id   BIGINT,
    is_muted  BOOLEAN,
    is_pinned BOOLEAN
);

CREATE TABLE IF NOT EXISTS conv.conv_bots (
    id                BIGINT PRIMARY KEY,
    conv_id           BIGINT,
    bot_id            BIGINT,
    added_by          BIGINT,
    response_triggers JSONB,
    bot_settings      TEXT,
    created_at        TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS conv.conv_bots_id_seq;
ALTER SEQUENCE conv.conv_bots_id_seq OWNED BY conv.conv_bots.id;

CREATE INDEX IF NOT EXISTS idx_conv_members_conv ON conv.conv_members(conv_id);
CREATE INDEX IF NOT EXISTS idx_conv_members_user ON conv.conv_members(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_members_pair ON conv.conv_members(conv_id, user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_read_seqs_pair ON conv.conv_read_seqs(conv_id, user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_settings_pair ON conv.conv_settings(conv_id, user_id);
CREATE INDEX IF NOT EXISTS idx_conv_bots_conv ON conv.conv_bots(conv_id);
CREATE INDEX IF NOT EXISTS idx_conv_bots_bot ON conv.conv_bots(bot_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_bots_pair ON conv.conv_bots(conv_id, bot_id);

-- =========== bot domain ===========

CREATE TABLE IF NOT EXISTS bot.bots (
    id                       BIGINT PRIMARY KEY,
    owner_id                 BIGINT DEFAULT 0 NOT NULL,
    name                     TEXT,
    avatar                   TEXT,
    type                     TEXT,
    pseudo_user_id           BIGINT,
    status                   TEXT DEFAULT 'active',
    use_platform_model       BOOLEAN DEFAULT TRUE NOT NULL,
    model_name               TEXT,
    model_id                 BIGINT DEFAULT 0 NOT NULL,
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
    memory_model_id          BIGINT DEFAULT 0,
    memory_limit             INTEGER DEFAULT 0 NOT NULL,
    memory_embedding_model_name TEXT,
    memory_embedding_model_id BIGINT DEFAULT 0,
    response_triggers        JSONB
);


CREATE TABLE IF NOT EXISTS bot.mcp_servers (
    id              BIGINT PRIMARY KEY,
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
    created_by      BIGINT DEFAULT 0 NOT NULL,
    created_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ
);
DO $$ BEGIN
    ALTER TABLE bot.mcp_servers ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
        SEQUENCE NAME bot.mcp_servers_id_seq
        START WITH 1 INCREMENT BY 1 NO MINVALUE NO MAXVALUE CACHE 1
    );
EXCEPTION WHEN duplicate_table THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS bot.bot_mcp_servers (
    id              BIGINT PRIMARY KEY,
    bot_id          BIGINT,
    mcp_server_id   BIGINT,
    enabled         BOOLEAN DEFAULT TRUE NOT NULL,
    config_override JSONB,
    created_at      TIMESTAMPTZ
);
DO $$ BEGIN
    ALTER TABLE bot.bot_mcp_servers ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY (
        SEQUENCE NAME bot.bot_mcp_servers_id_seq
        START WITH 1 INCREMENT BY 1 NO MINVALUE NO MAXVALUE CACHE 1
    );
EXCEPTION WHEN duplicate_table THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS bot.mcp_tools (
    id            BIGINT PRIMARY KEY,
    mcp_server_id BIGINT,
    name          TEXT,
    description   TEXT,
    input_schema  TEXT,
    created_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS bot.mcp_tools_id_seq;
ALTER SEQUENCE bot.mcp_tools_id_seq OWNED BY bot.mcp_tools.id;

CREATE TABLE IF NOT EXISTS bot.conv_summaries (
    id            BIGINT PRIMARY KEY,
    conv_id       BIGINT NOT NULL,
    user_id       BIGINT NOT NULL,
    range_type    VARCHAR(20) NOT NULL,
    range_start   BIGINT,
    range_end     BIGINT,
    message_count INTEGER DEFAULT 0 NOT NULL,
    summary       TEXT NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW() NOT NULL
);
CREATE SEQUENCE IF NOT EXISTS bot.conv_summaries_id_seq;
ALTER SEQUENCE bot.conv_summaries_id_seq OWNED BY bot.conv_summaries.id;

CREATE TABLE IF NOT EXISTS bot.summary_todos (
    id          BIGINT PRIMARY KEY,
    summary_id  BIGINT NOT NULL,
    conv_id     BIGINT NOT NULL,
    content     TEXT NOT NULL,
    done        BOOLEAN DEFAULT FALSE NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    updated_at  TIMESTAMPTZ DEFAULT NOW() NOT NULL
);
CREATE SEQUENCE IF NOT EXISTS bot.summary_todos_id_seq;
ALTER SEQUENCE bot.summary_todos_id_seq OWNED BY bot.summary_todos.id;

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

ALTER TABLE bot.summary_todos DROP CONSTRAINT IF EXISTS summary_todos_summary_id_fkey;
ALTER TABLE bot.summary_todos ADD CONSTRAINT summary_todos_summary_id_fkey
    FOREIGN KEY (summary_id) REFERENCES bot.conv_summaries(id) ON DELETE CASCADE;

-- =========== friend domain ===========

CREATE TABLE IF NOT EXISTS friend.friends (
    id         BIGINT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    friend_id  BIGINT NOT NULL,
    group_id   BIGINT DEFAULT 0 NOT NULL,
    remark     VARCHAR(64) DEFAULT '' NOT NULL,
    created_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS friend.friend_groups (
    id         BIGINT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    name       VARCHAR(64) NOT NULL,
    sort_order INTEGER DEFAULT 0 NOT NULL,
    created_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS friend.friend_requests (
    id           BIGINT PRIMARY KEY,
    from_user_id BIGINT NOT NULL,
    to_user_id   BIGINT NOT NULL,
    message      VARCHAR(256) DEFAULT '' NOT NULL,
    status       SMALLINT DEFAULT 0 NOT NULL,
    created_at   TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS friend.user_blocks (
    id              BIGINT PRIMARY KEY,
    user_id         BIGINT NOT NULL,
    blocked_user_id BIGINT NOT NULL,
    created_at      TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS friend.user_blocks_id_seq;
ALTER SEQUENCE friend.user_blocks_id_seq OWNED BY friend.user_blocks.id;

CREATE INDEX IF NOT EXISTS idx_friends_user ON friend.friends(user_id);
CREATE INDEX IF NOT EXISTS idx_friends_friend ON friend.friends(friend_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_friends_pair ON friend.friends(user_id, friend_id);
CREATE INDEX IF NOT EXISTS idx_friend_groups_user ON friend.friend_groups(user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_from ON friend.friend_requests(from_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_to ON friend.friend_requests(to_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_status ON friend.friend_requests(status);

-- =========== file domain ===========

CREATE TABLE IF NOT EXISTS file.files (
    id          BIGINT PRIMARY KEY,
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
    uploader_id BIGINT DEFAULT 0 NOT NULL,
    bucket      VARCHAR(128) DEFAULT 'aim' NOT NULL,
    created_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_files_uploader ON file.files(uploader_id);
CREATE INDEX IF NOT EXISTS idx_files_key ON file.files(key);

-- =========== knowledge domain ===========

CREATE TABLE IF NOT EXISTS knowledge.knowledge_bases (
    id                  BIGINT PRIMARY KEY,
    owner_id            BIGINT,
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
    embedding_model_id  BIGINT,
    last_maintenance_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS knowledge.documents (
    id                BIGINT PRIMARY KEY,
    kb_id             BIGINT,
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
    id              BIGINT PRIMARY KEY,
    doc_id          BIGINT,
    kb_id           BIGINT,
    chunk_index     BIGINT,
    content         TEXT,
    token_count     BIGINT,
    milvus_doc_id   TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ,
    parent_chunk_id BIGINT
);

CREATE TABLE IF NOT EXISTS knowledge.knowledge_bindings (
    id          BIGINT PRIMARY KEY,
    kb_id       BIGINT,
    target_type TEXT,
    target_id   BIGINT,
    created_at  TIMESTAMPTZ,
    kb_name     TEXT
);

CREATE TABLE IF NOT EXISTS knowledge.wiki_pages (
    id                BIGINT PRIMARY KEY,
    knowledge_base_id BIGINT NOT NULL,
    slug              VARCHAR(255) NOT NULL,
    title             VARCHAR(512) NOT NULL,
    page_type         VARCHAR(32) NOT NULL,
    status            VARCHAR(32) DEFAULT 'published' NOT NULL,
    content           TEXT NOT NULL,
    summary           TEXT,
    aliases           JSONB DEFAULT '[]'::JSONB,
    source_refs       JSONB DEFAULT '[]'::JSONB,
    chunk_refs        JSONB DEFAULT '[]'::JSONB,
    in_links          JSONB DEFAULT '[]'::JSONB,
    out_links         JSONB DEFAULT '[]'::JSONB,
    page_metadata     JSONB DEFAULT '{}'::JSONB,
    version           BIGINT DEFAULT 1 NOT NULL,
    created_at        TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ,
    deleted_at        TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS knowledge.wiki_pages_id_seq;
ALTER SEQUENCE knowledge.wiki_pages_id_seq OWNED BY knowledge.wiki_pages.id;

CREATE TABLE IF NOT EXISTS knowledge.wiki_page_issues (
    id                BIGINT PRIMARY KEY,
    knowledge_base_id BIGINT NOT NULL,
    page_slug         VARCHAR(255) NOT NULL,
    issue_type        VARCHAR(64) NOT NULL,
    level             VARCHAR(16) DEFAULT 'warning' NOT NULL,
    title             VARCHAR(512) NOT NULL,
    description       TEXT,
    status            VARCHAR(16) DEFAULT 'open' NOT NULL,
    created_at        TIMESTAMPTZ,
    resolved_at       TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS knowledge.wiki_page_issues_id_seq;
ALTER SEQUENCE knowledge.wiki_page_issues_id_seq OWNED BY knowledge.wiki_page_issues.id;

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_owner ON knowledge.knowledge_bases(owner_id);
CREATE INDEX IF NOT EXISTS idx_documents_kb ON knowledge.documents(kb_id);
CREATE INDEX IF NOT EXISTS idx_documents_status ON knowledge.documents(status);
CREATE INDEX IF NOT EXISTS idx_document_chunks_doc ON knowledge.document_chunks(doc_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_kb ON knowledge.document_chunks(kb_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_parent_chunk_id ON knowledge.document_chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bindings_kb ON knowledge.knowledge_bindings(kb_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bindings_target ON knowledge.knowledge_bindings(target_type, target_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_bindings_pair ON knowledge.knowledge_bindings(kb_id, target_type, target_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_slug ON knowledge.wiki_pages(knowledge_base_id, slug);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_deleted_at ON knowledge.wiki_pages(deleted_at);
CREATE INDEX IF NOT EXISTS idx_wiki_pages_page_type ON knowledge.wiki_pages(page_type);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_knowledge_base_id ON knowledge.wiki_page_issues(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_page_slug ON knowledge.wiki_page_issues(page_slug);

-- =========== llm domain ===========

CREATE TABLE IF NOT EXISTS llm.model_registry (
    id                    BIGINT PRIMARY KEY,
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
    owner_id              BIGINT,
    metadata              TEXT,
    created_at            TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS llm.billing_records (
    id            BIGINT PRIMARY KEY DEFAULT nextval('llm.billing_records_id_seq'::regclass),
    bot_id        BIGINT,
    owner_id      BIGINT,
    model_name    TEXT,
    capability    TEXT,
    input_tokens  BIGINT,
    output_tokens BIGINT,
    input_cost    NUMERIC,
    output_cost   NUMERIC,
    provider      TEXT,
    created_at    TIMESTAMPTZ
);
CREATE SEQUENCE IF NOT EXISTS llm.billing_records_id_seq;
ALTER SEQUENCE llm.billing_records_id_seq OWNED BY llm.billing_records.id;

CREATE SEQUENCE IF NOT EXISTS public.model_registry_id_seq;

CREATE INDEX IF NOT EXISTS idx_model_registry_provider ON llm.model_registry(provider);
CREATE INDEX IF NOT EXISTS idx_model_registry_status ON llm.model_registry(status);
CREATE INDEX IF NOT EXISTS idx_model_registry_owner ON llm.model_registry(owner_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_bot ON llm.billing_records(bot_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_owner ON llm.billing_records(owner_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_time ON llm.billing_records(created_at);

-- =========== audit domain ===========

CREATE TABLE IF NOT EXISTS audit.audit_events (
    id              BIGINT PRIMARY KEY DEFAULT nextval('audit.audit_events_id_seq'::regclass),
    event_id        VARCHAR(128) NOT NULL,
    action          INTEGER NOT NULL,
    result          INTEGER NOT NULL,
    risk            INTEGER NOT NULL,
    user_id         BIGINT NOT NULL,
    device_id       VARCHAR(256) DEFAULT '' NOT NULL,
    ip_address      VARCHAR(64) DEFAULT '' NOT NULL,
    user_agent      TEXT DEFAULT '' NOT NULL,
    resource_type   VARCHAR(64) DEFAULT '' NOT NULL,
    resource_id     VARCHAR(128) DEFAULT '' NOT NULL,
    detail          JSONB DEFAULT '{}'::JSONB NOT NULL,
    error_message   TEXT DEFAULT '' NOT NULL,
    trace_id        VARCHAR(128) DEFAULT '' NOT NULL,
    span_id         VARCHAR(128) DEFAULT '' NOT NULL,
    service_name    VARCHAR(128) DEFAULT '' NOT NULL,
    service_version VARCHAR(32) DEFAULT '' NOT NULL,
    review_status   SMALLINT DEFAULT 0 NOT NULL,
    review_result   JSONB DEFAULT '{}'::JSONB NOT NULL,
    is_archived     BOOLEAN DEFAULT FALSE NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW() NOT NULL
);
CREATE SEQUENCE IF NOT EXISTS audit.audit_events_id_seq;
ALTER SEQUENCE audit.audit_events_id_seq OWNED BY audit.audit_events.id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_events_event_id ON audit.audit_events(event_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_user_id ON audit.audit_events(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit.audit_events(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit.audit_events(action);

-- =========== notify domain ===========

CREATE TABLE IF NOT EXISTS notify.notifications (
    id           BIGINT PRIMARY KEY,
    user_id      BIGINT,
    type         INTEGER,
    title        TEXT,
    content      TEXT,
    is_read      BOOLEAN,
    reference_id TEXT,
    created_at   BIGINT
);

CREATE TABLE IF NOT EXISTS notify.device_tokens (
    id        BIGINT PRIMARY KEY,
    user_id   BIGINT,
    device_id VARCHAR(128),
    platform  VARCHAR(16),
    token     VARCHAR(512),
    provider  VARCHAR(8),
    created_at BIGINT,
    updated_at BIGINT
);
CREATE SEQUENCE IF NOT EXISTS notify.device_tokens_id_seq;
ALTER SEQUENCE notify.device_tokens_id_seq OWNED BY notify.device_tokens.id;

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notify.notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read ON notify.notifications(user_id, is_read);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_device ON notify.device_tokens(user_id, device_id);
