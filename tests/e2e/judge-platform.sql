-- Isolated harness setup uses the platform operator's database registration seam.
-- Ciphertext encrypts only the public fixture key with run.py's isolated AIM_ENC_KEY.
INSERT INTO llm.quota_pools (id, name, owner_type) VALUES
('01900000-0000-7000-8000-000000000011', 'e2e platform judge quota', 'platform'),
('01900000-0000-7000-8000-000000000012', 'e2e inactive judge quota', 'platform');
INSERT INTO llm.provider_connections
(id, name, provider, base_url, api_key_encrypted, quota_pool_id, owner_type) VALUES
('01900000-0000-7000-8000-000000000011', 'e2e platform judge', 'typesafe', 'http://e2e-provider:8099/native',
 'Rds09HlI5CixyUgbC4XjWJkIULqEOK0qimHdtFT0P4QhUdeIehupMlhWE/Ccp2zYJHPnAg==', '01900000-0000-7000-8000-000000000011', 'platform'),
('01900000-0000-7000-8000-000000000012', 'e2e inactive judge', 'typesafe', 'http://e2e-provider:8099/native',
 'Rds09HlI5CixyUgbC4XjWJkIULqEOK0qimHdtFT0P4QhUdeIehupMlhWE/Ccp2zYJHPnAg==', '01900000-0000-7000-8000-000000000012', 'platform');
INSERT INTO llm.model_registry
(id, model_name, connection_id, capability, input_price_per_mtok, output_price_per_mtok, status, owner_type, created_at, updated_at) VALUES
('01900000-0000-7000-8000-000000000011', 'e2e-component-judge-platform-2026-10-01', '01900000-0000-7000-8000-000000000011', 'judge', 2, 0, 'active', 'platform', NOW(), NOW()),
('01900000-0000-7000-8000-000000000012', 'e2e-component-judge-inactive-2026-10-01', '01900000-0000-7000-8000-000000000012', 'judge', 2, 0, 'inactive', 'platform', NOW(), NOW());
