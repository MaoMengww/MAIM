-- Provider identity and credentials belong to connections, not callable models.
-- Every legacy row gets its own connection AND pool, including disabled/platform
-- rows. Equal names/credentials are deliberately never treated as shared supply.
DO $model_connections$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 30);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '030_model_connections') THEN
        RETURN;
    END IF;

CREATE TABLE llm.quota_pools (
    id UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name TEXT NOT NULL,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('platform', 'user')),
    owner_id UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    owner_scope TEXT GENERATED ALWAYS AS (COALESCE(owner_id::text, 'platform')) STORED,
    requests_per_minute BIGINT NOT NULL DEFAULT 0 CHECK (requests_per_minute >= 0),
    requests_per_second BIGINT NOT NULL DEFAULT 0 CHECK (requests_per_second >= 0),
    tokens_per_second BIGINT NOT NULL DEFAULT 0 CHECK (tokens_per_second >= 0),
    concurrency BIGINT NOT NULL DEFAULT 0 CHECK (concurrency >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT quota_pools_ownership CHECK ((owner_type = 'platform' AND owner_id IS NULL) OR (owner_type = 'user' AND owner_id IS NOT NULL)),
    UNIQUE (id, owner_type, owner_scope)
);
CREATE TABLE llm.provider_connections (
    id UUID PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    api_key_encrypted TEXT NOT NULL DEFAULT '',
    quota_pool_id UUID NOT NULL,
    owner_type TEXT NOT NULL CHECK (owner_type IN ('platform', 'user')),
    owner_id UUID CHECK (owner_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    owner_scope TEXT GENERATED ALWAYS AS (COALESCE(owner_id::text, 'platform')) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT provider_connections_ownership CHECK ((owner_type = 'platform' AND owner_id IS NULL) OR (owner_type = 'user' AND owner_id IS NOT NULL)),
    UNIQUE (id, owner_type, owner_scope),
    FOREIGN KEY (quota_pool_id, owner_type, owner_scope) REFERENCES llm.quota_pools(id, owner_type, owner_scope)
);
INSERT INTO llm.quota_pools (id, name, owner_type, owner_id)
SELECT id, COALESCE(model_name, 'Migrated model') || ' quota', owner_type, owner_id FROM llm.model_registry;
INSERT INTO llm.provider_connections (id, name, provider, base_url, api_key_encrypted, quota_pool_id, owner_type, owner_id)
SELECT id, COALESCE(model_name, 'Migrated model'), COALESCE(provider, ''), COALESCE(base_url, ''), COALESCE(api_key_encrypted, ''), id, owner_type, owner_id FROM llm.model_registry;
ALTER TABLE llm.model_registry
    ADD COLUMN connection_id UUID,
    ADD COLUMN owner_scope TEXT GENERATED ALWAYS AS (COALESCE(owner_id::text, 'platform')) STORED,
    ADD COLUMN requests_per_minute BIGINT NOT NULL DEFAULT 0 CHECK (requests_per_minute >= 0),
    ADD COLUMN requests_per_second BIGINT NOT NULL DEFAULT 0 CHECK (requests_per_second >= 0),
    ADD COLUMN tokens_per_second BIGINT NOT NULL DEFAULT 0 CHECK (tokens_per_second >= 0),
    ADD COLUMN concurrency BIGINT NOT NULL DEFAULT 0 CHECK (concurrency >= 0);
UPDATE llm.model_registry SET connection_id = id;
ALTER TABLE llm.model_registry
    ALTER COLUMN connection_id SET NOT NULL,
    ADD CONSTRAINT model_registry_connection_owner FOREIGN KEY (connection_id, owner_type, owner_scope) REFERENCES llm.provider_connections(id, owner_type, owner_scope),
    DROP COLUMN provider,
    DROP COLUMN base_url,
    DROP COLUMN api_key_encrypted;
CREATE INDEX idx_provider_connections_owner ON llm.provider_connections(owner_type, owner_id);
CREATE INDEX idx_provider_connections_pool ON llm.provider_connections(quota_pool_id);
CREATE INDEX idx_quota_pools_owner ON llm.quota_pools(owner_type, owner_id);
CREATE INDEX idx_model_registry_connection ON llm.model_registry(connection_id);

-- Capacity revisions must increase even under concurrent writes and replica clock
-- skew; scheduler ordering is based on persisted timestamps, not process clocks.
CREATE FUNCTION llm.advance_capacity_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := GREATEST(clock_timestamp(), COALESCE(OLD.updated_at, '-infinity'::timestamptz) + INTERVAL '1 microsecond');
    RETURN NEW;
END;
$$;
CREATE TRIGGER model_capacity_revision BEFORE UPDATE ON llm.model_registry
    FOR EACH ROW EXECUTE FUNCTION llm.advance_capacity_revision();
CREATE TRIGGER pool_capacity_revision BEFORE UPDATE ON llm.quota_pools
    FOR EACH ROW EXECUTE FUNCTION llm.advance_capacity_revision();

    INSERT INTO public.schema_migrations(version)
        VALUES ('030_model_connections') ON CONFLICT DO NOTHING;
END;
$model_connections$;
