-- AIM Database Schema
-- Consolidated starting point: all tables in domain schemas with final column definitions

-- =========== Create domain schemas ===========
CREATE SCHEMA IF NOT EXISTS bot;
CREATE SCHEMA IF NOT EXISTS conv;
CREATE SCHEMA IF NOT EXISTS msg;
CREATE SCHEMA IF NOT EXISTS "user";
CREATE SCHEMA IF NOT EXISTS friend;
CREATE SCHEMA IF NOT EXISTS file;
CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS knowledge;
CREATE SCHEMA IF NOT EXISTS notify;
CREATE SCHEMA IF NOT EXISTS llm;

-- =========== user domain ===========

CREATE TABLE IF NOT EXISTS "user".users (
    id              BIGINT PRIMARY KEY,
    username        VARCHAR(128) NOT NULL,
    password_hash   VARCHAR(256) NOT NULL DEFAULT '',
    phone           VARCHAR(32)  NOT NULL DEFAULT '',
    email           VARCHAR(128) NOT NULL DEFAULT '',
    avatar          VARCHAR(512) NOT NULL DEFAULT '',
    gender          SMALLINT     NOT NULL DEFAULT 0,
    bio             TEXT         NOT NULL DEFAULT '',
    birthday        BIGINT       NOT NULL DEFAULT 0,
    balance         DECIMAL(12,6) NOT NULL DEFAULT 0,
    settings        JSONB        NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON "user".users(username);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_phone   ON "user".users(phone) WHERE phone != '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email   ON "user".users(email) WHERE email != '';

CREATE TABLE IF NOT EXISTS "user".user_devices (
    id              BIGSERIAL    PRIMARY KEY,
    user_id         BIGINT       NOT NULL,
    device_id       VARCHAR(256) NOT NULL,
    platform        VARCHAR(32)  NOT NULL DEFAULT 'web',
    push_token      VARCHAR(512) NOT NULL DEFAULT '',
    ip              VARCHAR(64)  NOT NULL DEFAULT '',
    location        VARCHAR(256) NOT NULL DEFAULT '',
    last_active_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_devices_user   ON "user".user_devices(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_unique ON "user".user_devices(user_id, device_id);

CREATE TABLE IF NOT EXISTS "user".user_blocks (
    id              BIGSERIAL   PRIMARY KEY,
    user_id         BIGINT      NOT NULL,
    blocked_user_id BIGINT      NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_blocks_user    ON "user".user_blocks(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_blocks_pair ON "user".user_blocks(user_id, blocked_user_id);

-- =========== friend domain ===========

CREATE TABLE IF NOT EXISTS friend.friends (
    id          BIGINT      PRIMARY KEY,
    user_id     BIGINT      NOT NULL,
    friend_id   BIGINT      NOT NULL,
    group_id    BIGINT      NOT NULL DEFAULT 0,
    remark      VARCHAR(64) NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_friends_user   ON friend.friends(user_id);
CREATE INDEX IF NOT EXISTS idx_friends_friend ON friend.friends(friend_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_friends_pair ON friend.friends(user_id, friend_id);

CREATE TABLE IF NOT EXISTS friend.friend_groups (
    id          BIGINT      PRIMARY KEY,
    user_id     BIGINT      NOT NULL,
    name        VARCHAR(64) NOT NULL,
    sort_order  INT         NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_friend_groups_user ON friend.friend_groups(user_id);

CREATE TABLE IF NOT EXISTS friend.friend_requests (
    id           BIGINT       PRIMARY KEY,
    from_user_id BIGINT       NOT NULL,
    to_user_id   BIGINT       NOT NULL,
    message      VARCHAR(256) NOT NULL DEFAULT '',
    status       SMALLINT     NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_friend_requests_from   ON friend.friend_requests(from_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_to     ON friend.friend_requests(to_user_id);
CREATE INDEX IF NOT EXISTS idx_friend_requests_status ON friend.friend_requests(status);

-- =========== conv domain ===========

CREATE TABLE IF NOT EXISTS conv.conversations (
    id                   BIGINT       PRIMARY KEY,
    type                 SMALLINT     NOT NULL DEFAULT 0,
    name                 VARCHAR(256) NOT NULL DEFAULT '',
    avatar               VARCHAR(512) NOT NULL DEFAULT '',
    owner_id             BIGINT       NOT NULL DEFAULT 0,
    announcement         TEXT         NOT NULL DEFAULT '',
    is_muted_all         BOOLEAN      NOT NULL DEFAULT FALSE,
    background           VARCHAR(512) NOT NULL DEFAULT '',
    max_seq              BIGINT       NOT NULL DEFAULT 0,
    last_message_id      BIGINT       NOT NULL DEFAULT 0,
    last_message_preview TEXT         NOT NULL DEFAULT '',
    member_count         INT          NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS conv.conv_members (
    id          BIGSERIAL    PRIMARY KEY,
    conv_id     BIGINT       NOT NULL,
    user_id     BIGINT       NOT NULL,
    member_type VARCHAR(16)  NOT NULL DEFAULT 'user',
    bot_id      BIGINT       NOT NULL DEFAULT 0,
    role        SMALLINT     NOT NULL DEFAULT 0,
    alias       VARCHAR(128) NOT NULL DEFAULT '',
    is_muted    BOOLEAN      NOT NULL DEFAULT FALSE,
    mute_until  BIGINT       NOT NULL DEFAULT 0,
    joined_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_conv_members_conv ON conv.conv_members(conv_id);
CREATE INDEX IF NOT EXISTS idx_conv_members_user ON conv.conv_members(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_members_pair ON conv.conv_members(conv_id, user_id);

CREATE TABLE IF NOT EXISTS conv.conv_read_seqs (
    id            BIGINT      PRIMARY KEY,
    conv_id       BIGINT      NOT NULL,
    user_id       BIGINT      NOT NULL,
    last_read_seq BIGINT      NOT NULL DEFAULT 0,
    read_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_read_seqs_pair ON conv.conv_read_seqs(conv_id, user_id);

CREATE TABLE IF NOT EXISTS conv.conv_settings (
    id        BIGINT  PRIMARY KEY,
    conv_id   BIGINT  NOT NULL,
    user_id   BIGINT  NOT NULL,
    is_muted  BOOLEAN NOT NULL DEFAULT FALSE,
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_settings_pair ON conv.conv_settings(conv_id, user_id);

-- =========== msg domain ===========

CREATE TABLE IF NOT EXISTS msg.messages (
    id             BIGINT      PRIMARY KEY,
    conv_id        BIGINT      NOT NULL,
    sender_id      BIGINT      NOT NULL,
    sender_type    VARCHAR(16) NOT NULL DEFAULT 'user',
    client_msg_id  VARCHAR(64) NOT NULL DEFAULT '',
    seq            BIGINT      NOT NULL DEFAULT 0,
    msg_type       SMALLINT    NOT NULL DEFAULT 0,
    content        JSONB       NOT NULL DEFAULT '{}',
    reply_to_msg_id BIGINT     NOT NULL DEFAULT 0,
    status         SMALLINT    NOT NULL DEFAULT 1,
    edit_history   JSONB       NOT NULL DEFAULT '[]',
    edit_count     INT         NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_messages_conv_seq ON msg.messages(conv_id, seq);
CREATE INDEX IF NOT EXISTS idx_messages_sender   ON msg.messages(sender_id);
CREATE INDEX IF NOT EXISTS idx_messages_created  ON msg.messages(created_at);

CREATE TABLE IF NOT EXISTS msg.user_inbox (
    user_id       BIGINT      NOT NULL,
    conv_id       BIGINT      NOT NULL,
    message_id    BIGINT      NOT NULL,
    seq           BIGINT      NOT NULL,
    last_read_seq BIGINT      NOT NULL DEFAULT 0,
    is_deleted    BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, conv_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_user_inbox_conv ON msg.user_inbox(user_id, conv_id);

CREATE TABLE IF NOT EXISTS msg.broadcasts (
    id             BIGINT      PRIMARY KEY,
    sender_id      BIGINT      NOT NULL,
    content        JSONB       NOT NULL DEFAULT '{}',
    scope          VARCHAR(32) NOT NULL DEFAULT 'all',
    scope_target_id BIGINT     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS msg.sequences (
    conv_id     BIGINT PRIMARY KEY,
    current_seq BIGINT NOT NULL DEFAULT 0
);

-- =========== file domain ===========

CREATE TABLE IF NOT EXISTS file.files (
    id          BIGSERIAL    PRIMARY KEY,
    name        VARCHAR(512) NOT NULL DEFAULT '',
    key         VARCHAR(512) NOT NULL,
    size        BIGINT       NOT NULL DEFAULT 0,
    mime_type   VARCHAR(256) NOT NULL DEFAULT '',
    ext         VARCHAR(32)  NOT NULL DEFAULT '',
    width       INT          NOT NULL DEFAULT 0,
    height      INT          NOT NULL DEFAULT 0,
    duration    INT          NOT NULL DEFAULT 0,
    md5         VARCHAR(64)  NOT NULL DEFAULT '',
    purpose     SMALLINT     NOT NULL DEFAULT 0,
    access      SMALLINT     NOT NULL DEFAULT 0,
    uploader_id BIGINT       NOT NULL DEFAULT 0,
    bucket      VARCHAR(128) NOT NULL DEFAULT 'aim',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_files_uploader ON file.files(uploader_id);
CREATE INDEX IF NOT EXISTS idx_files_key      ON file.files(key);

-- =========== audit domain ===========

CREATE TABLE IF NOT EXISTS audit.audit_events (
    id              BIGSERIAL    PRIMARY KEY,
    event_id        VARCHAR(128) NOT NULL,
    action          INT          NOT NULL,
    result          INT          NOT NULL,
    risk            INT          NOT NULL,
    user_id         BIGINT       NOT NULL,
    device_id       VARCHAR(256) NOT NULL DEFAULT '',
    ip_address      VARCHAR(64)  NOT NULL DEFAULT '',
    user_agent      TEXT         NOT NULL DEFAULT '',
    resource_type   VARCHAR(64)  NOT NULL DEFAULT '',
    resource_id     VARCHAR(128) NOT NULL DEFAULT '',
    detail          JSONB        NOT NULL DEFAULT '{}',
    error_message   TEXT         NOT NULL DEFAULT '',
    trace_id        VARCHAR(128) NOT NULL DEFAULT '',
    span_id         VARCHAR(128) NOT NULL DEFAULT '',
    service_name    VARCHAR(128) NOT NULL DEFAULT '',
    service_version VARCHAR(32)  NOT NULL DEFAULT '',
    review_status   SMALLINT     NOT NULL DEFAULT 0,
    review_result   JSONB        NOT NULL DEFAULT '{}',
    is_archived     BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_events_event_id ON audit.audit_events(event_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_user_id   ON audit.audit_events(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_events_created   ON audit.audit_events(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_action    ON audit.audit_events(action);

-- =========== knowledge domain ===========

CREATE TABLE IF NOT EXISTS knowledge.knowledge_bases (
    id              BIGINT       PRIMARY KEY,
    owner_id        BIGINT       NOT NULL DEFAULT 0,
    name            VARCHAR(256) NOT NULL DEFAULT '',
    description     TEXT         NOT NULL DEFAULT '',
    embedding_model VARCHAR(128) NOT NULL DEFAULT 'text-embedding-3-small',
    pipeline_config JSONB        NOT NULL DEFAULT '{}',
    doc_count       INT          NOT NULL DEFAULT 0,
    total_chunks    INT          NOT NULL DEFAULT 0,
    status          VARCHAR(32)  NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_owner ON knowledge.knowledge_bases(owner_id);

CREATE TABLE IF NOT EXISTS knowledge.documents (
    id                BIGINT       PRIMARY KEY,
    kb_id             BIGINT       NOT NULL,
    title             VARCHAR(512) NOT NULL DEFAULT '',
    file_type         VARCHAR(32)  NOT NULL DEFAULT '',
    file_size         BIGINT       NOT NULL DEFAULT 0,
    original_filename VARCHAR(512) NOT NULL DEFAULT '',
    minio_bucket      VARCHAR(128) NOT NULL DEFAULT '',
    minio_key         VARCHAR(512) NOT NULL DEFAULT '',
    content_hash      VARCHAR(128) NOT NULL DEFAULT '',
    status            VARCHAR(32)  NOT NULL DEFAULT 'pending',
    chunk_count       INT          NOT NULL DEFAULT 0,
    error_message     TEXT         NOT NULL DEFAULT '',
    pipeline_override JSONB,
    metadata          JSONB        NOT NULL DEFAULT '{}',
    stages            JSONB        NOT NULL DEFAULT '[]',
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_documents_kb     ON knowledge.documents(kb_id);
CREATE INDEX IF NOT EXISTS idx_documents_status ON knowledge.documents(status);

CREATE TABLE IF NOT EXISTS knowledge.document_chunks (
    id           BIGINT       PRIMARY KEY,
    doc_id       BIGINT       NOT NULL,
    kb_id        BIGINT       NOT NULL,
    chunk_index  INT          NOT NULL DEFAULT 0,
    content      TEXT         NOT NULL DEFAULT '',
    token_count  INT          NOT NULL DEFAULT 0,
    milvus_doc_id VARCHAR(256) NOT NULL DEFAULT '',
    metadata     JSONB        NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_document_chunks_doc ON knowledge.document_chunks(doc_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_kb  ON knowledge.document_chunks(kb_id);

CREATE TABLE IF NOT EXISTS knowledge.knowledge_bindings (
    id          BIGSERIAL    PRIMARY KEY,
    kb_id       BIGINT       NOT NULL,
    target_type VARCHAR(32)  NOT NULL,
    target_id   BIGINT       NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_knowledge_bindings_kb     ON knowledge.knowledge_bindings(kb_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_bindings_target ON knowledge.knowledge_bindings(target_type, target_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_bindings_pair ON knowledge.knowledge_bindings(kb_id, target_type, target_id);

-- =========== bot domain ===========

CREATE TABLE IF NOT EXISTS bot.bots (
    id                       BIGINT        PRIMARY KEY,
    owner_id                 BIGINT        NOT NULL DEFAULT 0,
    name                     VARCHAR(128)  NOT NULL DEFAULT '',
    avatar                   VARCHAR(512)  NOT NULL DEFAULT '',
    type                     VARCHAR(32)   NOT NULL DEFAULT '',
    status                   VARCHAR(32)   NOT NULL DEFAULT 'active',
    use_platform_model       BOOLEAN       NOT NULL DEFAULT TRUE,
    model_name               VARCHAR(128)  NOT NULL DEFAULT '',
    model_id                 BIGINT        NOT NULL DEFAULT 0,
    base_url                 VARCHAR(512),
    api_key_encrypted        TEXT,
    system_prompt            TEXT          NOT NULL DEFAULT '',
    persona                  TEXT          NOT NULL DEFAULT '',
    enable_knowledge         BOOLEAN       NOT NULL DEFAULT TRUE,
    temperature              DOUBLE PRECISION NOT NULL DEFAULT 0.7,
    max_context_messages     INT           NOT NULL DEFAULT 10,
    streaming_enabled        BOOLEAN       NOT NULL DEFAULT TRUE,
    memory_model_name        VARCHAR(128)  NOT NULL DEFAULT '',
    memory_limit             INT           NOT NULL DEFAULT 0,
    memory_use_platform_model BOOLEAN      NOT NULL DEFAULT TRUE,
    memory_api_key_encrypted TEXT,
    conn_mode                VARCHAR(32),
    webhook_secret           VARCHAR(256),
    callback_url             VARCHAR(512),
    app_secret_hash          VARCHAR(256),
    bot_tags                 JSONB,
    capabilities             JSONB,
    settings                 JSONB,
    created_at               TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bots_owner       ON bot.bots(owner_id);
CREATE INDEX IF NOT EXISTS idx_bots_status      ON bot.bots(status);
CREATE INDEX IF NOT EXISTS idx_bots_type        ON bot.bots(type);
CREATE INDEX IF NOT EXISTS idx_bots_model_id    ON bot.bots(model_id);

CREATE TABLE IF NOT EXISTS conv.conv_bots (
    id                BIGINT      PRIMARY KEY,
    conv_id           BIGINT      NOT NULL,
    bot_id            BIGINT      NOT NULL,
    added_by          BIGINT      NOT NULL DEFAULT 0,
    response_triggers JSONB       NOT NULL DEFAULT '[]',
    bot_settings      JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_conv_bots_conv ON conv.conv_bots(conv_id);
CREATE INDEX IF NOT EXISTS idx_conv_bots_bot  ON conv.conv_bots(bot_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_bots_pair ON conv.conv_bots(conv_id, bot_id);

CREATE TABLE IF NOT EXISTS bot.mcp_servers (
    id          BIGSERIAL    PRIMARY KEY,
    name        VARCHAR(128) NOT NULL DEFAULT '',
    description TEXT         NOT NULL DEFAULT '',
    transport   VARCHAR(32)  NOT NULL DEFAULT 'http',
    url         VARCHAR(512) NOT NULL DEFAULT '',
    command     VARCHAR(512) NOT NULL DEFAULT '',
    args        TEXT[]       NOT NULL DEFAULT '{}',
    env         JSONB        NOT NULL DEFAULT '{}',
    discovery   VARCHAR(32)  NOT NULL DEFAULT 'dynamic',
    tools       TEXT[]       NOT NULL DEFAULT '{}',
    timeout_ms  INT          NOT NULL DEFAULT 10000,
    status      VARCHAR(32)  NOT NULL DEFAULT 'active',
    auth_config JSONB,
    advanced_config JSONB,
    enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
    created_by  BIGINT       NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mcp_servers_status ON bot.mcp_servers(status);

CREATE TABLE IF NOT EXISTS bot.bot_mcp_servers (
    id              BIGSERIAL   PRIMARY KEY,
    bot_id          BIGINT      NOT NULL,
    mcp_server_id   BIGINT      NOT NULL,
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    config_override JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bot_mcp_servers_bot ON bot.bot_mcp_servers(bot_id);
CREATE INDEX IF NOT EXISTS idx_bot_mcp_servers_mcp ON bot.bot_mcp_servers(mcp_server_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bot_mcp_servers_pair ON bot.bot_mcp_servers(bot_id, mcp_server_id);

CREATE TABLE IF NOT EXISTS bot.bot_memories (
    id               BIGSERIAL    PRIMARY KEY,
    bot_id           BIGINT       NOT NULL,
    user_id          BIGINT       NOT NULL,
    memory_type      VARCHAR(32)  NOT NULL DEFAULT 'fact',
    content          TEXT         NOT NULL DEFAULT '',
    importance       DOUBLE PRECISION NOT NULL DEFAULT 0,
    subject          VARCHAR(128) NOT NULL DEFAULT '',
    predicate        VARCHAR(128) NOT NULL DEFAULT '',
    object           TEXT         NOT NULL DEFAULT '',
    category         VARCHAR(64)  NOT NULL DEFAULT '',
    confidence       DOUBLE PRECISION NOT NULL DEFAULT 1,
    access_count     INT          NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ,
    milvus_id        VARCHAR(256) NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bot_memories_bot   ON bot.bot_memories(bot_id);
CREATE INDEX IF NOT EXISTS idx_bot_memories_user  ON bot.bot_memories(user_id);
CREATE INDEX IF NOT EXISTS idx_bot_memories_type  ON bot.bot_memories(memory_type);
CREATE INDEX IF NOT EXISTS idx_bot_memories_scope_category ON bot.bot_memories(bot_id, user_id, category);
CREATE UNIQUE INDEX IF NOT EXISTS uq_bot_memories_fact ON bot.bot_memories(bot_id, user_id, subject, predicate, object) WHERE memory_type = 'fact';

-- 会话工具：总结记录
CREATE TABLE IF NOT EXISTS bot.conv_summaries (
    id            BIGSERIAL PRIMARY KEY,
    conv_id       BIGINT NOT NULL,
    user_id       BIGINT NOT NULL,
    range_type    VARCHAR(20) NOT NULL,
    range_start   BIGINT,
    range_end     BIGINT,
    message_count INT NOT NULL DEFAULT 0,
    summary       TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_conv_summaries_conv ON bot.conv_summaries(conv_id, created_at DESC);

-- 会话工具：待办事项
CREATE TABLE IF NOT EXISTS bot.summary_todos (
    id          BIGSERIAL PRIMARY KEY,
    summary_id  BIGINT NOT NULL REFERENCES bot.conv_summaries(id) ON DELETE CASCADE,
    conv_id     BIGINT NOT NULL,
    content     TEXT NOT NULL,
    done        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_summary_todos_conv ON bot.summary_todos(conv_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_summary_todos_summary ON bot.summary_todos(summary_id);

-- =========== notify domain ===========

CREATE TABLE IF NOT EXISTS notify.notifications (
    id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    type INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    reference_id TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notify.notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read ON notify.notifications(user_id, is_read);

-- =========== llm domain ===========

CREATE TABLE IF NOT EXISTS llm.model_registry (
    id                  BIGINT           PRIMARY KEY,
    model_name          VARCHAR(128)     NOT NULL DEFAULT '',
    provider            VARCHAR(64)      NOT NULL DEFAULT '',
    capability          VARCHAR(32)      NOT NULL DEFAULT '',
    base_url            VARCHAR(512)     NOT NULL DEFAULT '',
    api_key_encrypted   TEXT             NOT NULL DEFAULT '',
    context_window      INT              NOT NULL DEFAULT 0,
    max_output_tokens   INT              NOT NULL DEFAULT 0,
    input_price_per_mtok  DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_price_per_mtok DOUBLE PRECISION NOT NULL DEFAULT 0,
    status              VARCHAR(32)      NOT NULL DEFAULT 'active',
    owner_id            BIGINT           NOT NULL DEFAULT 0,
    metadata            JSONB            NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_model_registry_provider ON llm.model_registry(provider);
CREATE INDEX IF NOT EXISTS idx_model_registry_status   ON llm.model_registry(status);
CREATE INDEX IF NOT EXISTS idx_model_registry_owner    ON llm.model_registry(owner_id);

CREATE TABLE IF NOT EXISTS llm.billing_records (
    id            BIGINT           PRIMARY KEY,
    bot_id        BIGINT           NOT NULL DEFAULT 0,
    owner_id      BIGINT           NOT NULL DEFAULT 0,
    model_name    VARCHAR(128)     NOT NULL DEFAULT '',
    capability    VARCHAR(32)      NOT NULL DEFAULT '',
    input_tokens  INT              NOT NULL DEFAULT 0,
    output_tokens INT              NOT NULL DEFAULT 0,
    input_cost    DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_cost   DOUBLE PRECISION NOT NULL DEFAULT 0,
    provider      VARCHAR(64)      NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_billing_records_bot   ON llm.billing_records(bot_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_owner ON llm.billing_records(owner_id);
CREATE INDEX IF NOT EXISTS idx_billing_records_time  ON llm.billing_records(created_at);
