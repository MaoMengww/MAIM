-- New terminal transitions only: no historical backfill and no model work in the Run transaction.
DO $run_reviews$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '022_run_reviews') THEN
        RETURN;
    END IF;

    CREATE TABLE bot.run_review_policies (
        bot_id UUID NOT NULL,
        scope_key TEXT NOT NULL,
        binding_id UUID NOT NULL,
        personal_user_id UUID,
        enabled BOOLEAN NOT NULL DEFAULT TRUE,
        revision BIGINT NOT NULL DEFAULT 1,
        updated_by UUID NOT NULL,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        PRIMARY KEY (bot_id, scope_key)
    );
    CREATE TABLE bot.run_reviews (
        id UUID PRIMARY KEY,
        run_id UUID NOT NULL UNIQUE REFERENCES bot.runs(id),
        bot_id UUID NOT NULL,
        conv_id UUID NOT NULL,
        binding_id UUID NOT NULL,
        scope_key TEXT NOT NULL,
        personal_user_id UUID,
        publisher_id UUID,
        status TEXT NOT NULL CHECK (status IN ('queued','assessing','generating','no_candidate','candidate_generated','failed','over_budget','cancelled')),
        reason TEXT NOT NULL DEFAULT '',
        assessment JSONB NOT NULL DEFAULT '{}'::jsonb,
        model_calls INTEGER NOT NULL DEFAULT 0 CHECK (model_calls BETWEEN 0 AND 2),
        model_version TEXT NOT NULL DEFAULT '',
        candidate_id UUID REFERENCES bot.skill_candidates(id),
        evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
        evidence_digest TEXT NOT NULL DEFAULT '',
        expires_at TIMESTAMPTZ NOT NULL,
        started_at TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    CREATE INDEX idx_run_reviews_scope ON bot.run_reviews(bot_id, scope_key, created_at DESC);
    ALTER TABLE bot.skill_packages ADD COLUMN scope_key TEXT NOT NULL DEFAULT 'bot';
    ALTER TABLE bot.skill_packages ADD COLUMN goal_key TEXT NOT NULL DEFAULT '';
    ALTER TABLE bot.skill_packages DROP CONSTRAINT skill_packages_bot_id_publisher_id_name_key;
    ALTER TABLE bot.skill_packages ADD CONSTRAINT skill_packages_scoped_name UNIQUE(bot_id,publisher_id,scope_key,goal_key,name);
    CREATE UNIQUE INDEX idx_skill_package_review_goal ON bot.skill_packages(bot_id,publisher_id,scope_key,goal_key) WHERE scope_key <> 'bot' AND goal_key <> '';
    ALTER TABLE bot.skill_candidates ADD COLUMN source_review_id UUID REFERENCES bot.run_reviews(id);
    ALTER TABLE bot.skill_candidates ADD COLUMN review_content JSONB;
    CREATE UNIQUE INDEX idx_skill_review_candidate ON bot.skill_candidates(source_review_id) WHERE source_review_id IS NOT NULL;
    ALTER TABLE bot.run_outbox DROP CONSTRAINT run_outbox_kind_check;
    ALTER TABLE bot.run_outbox ADD CONSTRAINT run_outbox_kind_check CHECK (kind IN ('execute','updated','output','deliver','review'));

    INSERT INTO public.schema_migrations(version) VALUES ('022_run_reviews') ON CONFLICT DO NOTHING;
END;
$run_reviews$;

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
    INSERT INTO bot.run_reviews(id,run_id,bot_id,conv_id,binding_id,scope_key,personal_user_id,publisher_id,status,reason,expires_at)
    VALUES (review_id,NEW.id,NEW.bot_id,NEW.conv_id,NEW.binding_id,scope_identity,NEW.personal_user_id,
        (SELECT owner_id FROM bot.bots WHERE id = NEW.bot_id AND owner_type = 'user'),
        CASE WHEN COALESCE(review_enabled,TRUE) THEN 'queued' ELSE 'cancelled' END,
        CASE WHEN COALESCE(review_enabled,TRUE) THEN '' ELSE 'scope review disabled' END,
        NOW() + INTERVAL '30 days') ON CONFLICT (run_id) DO NOTHING;
    IF FOUND AND COALESCE(review_enabled,TRUE) THEN
        INSERT INTO bot.run_outbox(id,run_id,kind,version,payload,available_at)
        VALUES (gen_random_uuid(),NEW.id,'review',NEW.version,'{}',NOW()) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END;
$review$;
DROP TRIGGER IF EXISTS queue_terminal_run_review ON bot.runs;
CREATE TRIGGER queue_terminal_run_review AFTER UPDATE OF status ON bot.runs FOR EACH ROW EXECUTE FUNCTION bot.queue_terminal_run_review();
