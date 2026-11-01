-- Preserve independently reported usage: NULL means unknown, zero means reported zero.
-- Existing token values are retained; historical zeros cannot be inferred as unknown.
DO $nullable_billing_tokens$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 29);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '029_nullable_billing_tokens') THEN
        RETURN;
    END IF;

    ALTER TABLE llm.billing_records
        ALTER COLUMN input_tokens DROP NOT NULL,
        ALTER COLUMN input_tokens DROP DEFAULT,
        ALTER COLUMN output_tokens DROP NOT NULL,
        ALTER COLUMN output_tokens DROP DEFAULT;

    COMMENT ON COLUMN llm.billing_records.input_tokens IS
        'Provider-reported input tokens; NULL means not reported, zero means reported zero.';
    COMMENT ON COLUMN llm.billing_records.output_tokens IS
        'Provider-reported output tokens; NULL means not reported, zero means reported zero.';

    INSERT INTO public.schema_migrations(version)
        VALUES ('029_nullable_billing_tokens') ON CONFLICT DO NOTHING;
END;
$nullable_billing_tokens$;
