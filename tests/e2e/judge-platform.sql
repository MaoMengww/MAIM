-- Isolated harness setup uses the platform operator's database registration seam.
-- Ciphertext encrypts only the public fixture key with run.py's isolated AIM_ENC_KEY.
INSERT INTO llm.model_registry (
    id, model_name, provider, capability, base_url, api_key_encrypted,
    input_price_per_mtok, output_price_per_mtok, status, owner_type,
    created_at, updated_at
) VALUES (
    '01900000-0000-7000-8000-000000000011',
    'e2e-component-judge-platform-2026-10-01', 'typesafe', 'judge',
    'http://e2e-provider:8099/native',
    'Rds09HlI5CixyUgbC4XjWJkIULqEOK0qimHdtFT0P4QhUdeIehupMlhWE/Ccp2zYJHPnAg==',
    2, 0, 'active', 'platform', NOW(), NOW()
);

INSERT INTO llm.model_registry (
    id, model_name, provider, capability, base_url, api_key_encrypted,
    input_price_per_mtok, output_price_per_mtok, status, owner_type,
    created_at, updated_at
) SELECT '01900000-0000-7000-8000-000000000012',
    'e2e-component-judge-inactive-2026-10-01', provider, capability,
    base_url, api_key_encrypted, input_price_per_mtok, output_price_per_mtok,
    'inactive', owner_type, NOW(), NOW()
  FROM llm.model_registry WHERE id = '01900000-0000-7000-8000-000000000011';
