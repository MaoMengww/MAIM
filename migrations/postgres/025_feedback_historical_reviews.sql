-- Delayed feedback and explicit historical review are independent source-bound
-- revisions. They must never overwrite the automatic review or its candidate.
DO $feedback_reviews$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 25);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '025_feedback_historical_reviews') THEN
        RETURN;
    END IF;

    ALTER TABLE bot.run_reviews DROP CONSTRAINT IF EXISTS run_reviews_run_id_key;
    ALTER TABLE bot.run_reviews ADD COLUMN revision BIGINT NOT NULL DEFAULT 1;
    ALTER TABLE bot.run_reviews ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'automatic' CHECK (trigger_kind IN ('automatic','feedback','historical'));
    ALTER TABLE bot.run_reviews ADD COLUMN submitted_by UUID;
    ALTER TABLE bot.run_reviews ADD COLUMN parent_review_id UUID REFERENCES bot.run_reviews(id);
    ALTER TABLE bot.run_reviews ADD COLUMN feedback TEXT NOT NULL DEFAULT '';
    ALTER TABLE bot.run_reviews ADD CONSTRAINT run_reviews_run_revision_key UNIQUE(run_id, revision);
    CREATE INDEX idx_run_reviews_parent ON bot.run_reviews(parent_review_id) WHERE parent_review_id IS NOT NULL;
    CREATE INDEX idx_run_reviews_feedback_expiry ON bot.run_reviews(expires_at, id) WHERE feedback <> '';

    ALTER TABLE bot.run_outbox DROP CONSTRAINT IF EXISTS run_outbox_run_id_kind_version_key;
    UPDATE bot.run_outbox o SET payload = jsonb_build_object('review_id', r.id)
        FROM bot.run_reviews r WHERE o.kind = 'review' AND o.run_id = r.run_id;
    CREATE UNIQUE INDEX idx_run_outbox_non_review_unique ON bot.run_outbox(run_id, kind, version) WHERE kind <> 'review';
    CREATE UNIQUE INDEX idx_run_outbox_review_unique ON bot.run_outbox(run_id, kind, (payload->>'review_id')) WHERE kind = 'review';

    INSERT INTO public.schema_migrations(version) VALUES ('025_feedback_historical_reviews') ON CONFLICT DO NOTHING;
END;
$feedback_reviews$;

CREATE OR REPLACE FUNCTION bot.queue_terminal_run_review() RETURNS trigger LANGUAGE plpgsql AS $review$
DECLARE
    review_id UUID;
    scope_identity TEXT;
    review_enabled BOOLEAN;
BEGIN
    IF NEW.status NOT IN ('succeeded','failed','stopped') OR OLD.status IN ('succeeded','failed','stopped')
        OR NEW.trigger_kind IN ('review','validation') THEN
        RETURN NEW;
    END IF;
    scope_identity := CASE WHEN NEW.visibility = 'personal' THEN 'personal:' || NEW.personal_user_id::text || ':' || NEW.binding_id::text ELSE 'conversation:' || NEW.binding_id::text END;
    SELECT enabled INTO review_enabled FROM bot.run_review_policies WHERE bot_id = NEW.bot_id AND scope_key = scope_identity;
    review_id := gen_random_uuid();
    INSERT INTO bot.run_reviews(id,run_id,bot_id,conv_id,binding_id,scope_key,personal_user_id,publisher_id,revision,trigger_kind,status,reason,expires_at)
    VALUES (review_id,NEW.id,NEW.bot_id,NEW.conv_id,NEW.binding_id,scope_identity,NEW.personal_user_id,
        (SELECT owner_id FROM bot.bots WHERE id = NEW.bot_id AND owner_type = 'user'),
        1, 'automatic', CASE WHEN COALESCE(review_enabled,TRUE) THEN 'queued' ELSE 'cancelled' END,
        CASE WHEN COALESCE(review_enabled,TRUE) THEN '' ELSE 'scope review disabled' END,
        NOW() + INTERVAL '30 days') ON CONFLICT (run_id,revision) DO NOTHING;
    IF FOUND AND COALESCE(review_enabled,TRUE) THEN
        INSERT INTO bot.run_outbox(id,run_id,kind,version,payload,available_at)
        VALUES (gen_random_uuid(),NEW.id,'review',1,jsonb_build_object('review_id', review_id),NOW()) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END;
$review$;
