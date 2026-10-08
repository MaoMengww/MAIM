-- Third-party work shares bot.runs and its transactional outbox.
DO $executor_run$
BEGIN
 PERFORM pg_advisory_xact_lock(4278605, 1);
 IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version='006_third_party_run') THEN RETURN; END IF;
 ALTER TABLE bot.bots ADD COLUMN executor_revision BIGINT NOT NULL DEFAULT 1, ADD COLUMN credentials_revoked_at TIMESTAMPTZ;
 CREATE TABLE bot.issued_tokens(token_id TEXT PRIMARY KEY,bot_id UUID NOT NULL,issued_at TIMESTAMPTZ NOT NULL);
 CREATE INDEX issued_tokens_bot ON bot.issued_tokens(bot_id);
 UPDATE bot.bots SET capabilities = '{"input_types":["text"],"supports_input":false,"supports_stop":false,"supports_resume":false}'::jsonb WHERE type='third_party' AND NOT (COALESCE(capabilities,'{}'::jsonb) ? 'input_types');
 CREATE OR REPLACE FUNCTION public.bot_executor_revision() RETURNS trigger LANGUAGE plpgsql AS $revision$
 BEGIN
  IF ROW(NEW.conn_mode,NEW.sub_type,NEW.callback_url,NEW.capabilities,NEW.webhook_secret,NEW.app_secret_hash,NEW.owner_type,NEW.owner_id,NEW.type,NEW.status) IS DISTINCT FROM ROW(OLD.conn_mode,OLD.sub_type,OLD.callback_url,OLD.capabilities,OLD.webhook_secret,OLD.app_secret_hash,OLD.owner_type,OLD.owner_id,OLD.type,OLD.status) THEN
   NEW.executor_revision := OLD.executor_revision + 1;
  END IF;
  RETURN NEW;
 END;
 $revision$;
 CREATE TRIGGER bot_executor_revision BEFORE UPDATE ON bot.bots FOR EACH ROW EXECUTE FUNCTION public.bot_executor_revision();
 ALTER TABLE bot.runs DROP CONSTRAINT runs_status_check;
 ALTER TABLE bot.runs ADD CONSTRAINT runs_status_check CHECK(status IN ('accepted','running','waiting_input','stopping','stopped','succeeded','failed'));
 ALTER TABLE bot.runs
  ADD COLUMN executor_type TEXT NOT NULL DEFAULT 'builtin' CHECK(executor_type IN ('builtin','third_party')),
  ADD COLUMN executor_capabilities JSONB NOT NULL DEFAULT '{"input_types":["text"],"supports_input":false,"supports_stop":false,"supports_resume":false}',
  ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'pending' CHECK(delivery_status IN ('pending','delivered','acknowledged')),
  ADD COLUMN executor_status TEXT NOT NULL DEFAULT 'pending' CHECK(executor_status IN ('pending','accepted','completed')),
  ADD COLUMN executor_sequence BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN control_operation TEXT NOT NULL DEFAULT '',
  ADD COLUMN input_sources JSONB NOT NULL DEFAULT '[]',
  ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'explicit',
  ADD COLUMN result_type TEXT NOT NULL DEFAULT '',
  ADD COLUMN result_content JSONB;
 ALTER TABLE bot.run_outbox DROP CONSTRAINT run_outbox_kind_check;
 ALTER TABLE bot.run_outbox ADD CONSTRAINT run_outbox_kind_check CHECK(kind IN ('execute','updated','output','deliver'));
 CREATE TABLE bot.run_receipts (
  id UUID PRIMARY KEY,
  run_id UUID NOT NULL REFERENCES bot.runs(id),
  update_key TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  receipt JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(run_id,update_key)
 );
 UPDATE bot.runs SET executor_status=CASE WHEN status='succeeded' THEN 'completed' WHEN status='running' THEN 'accepted' ELSE 'pending' END,delivery_status=CASE WHEN execution_generation>0 THEN 'acknowledged' ELSE 'pending' END;
 INSERT INTO public.schema_migrations(version) VALUES('006_third_party_run') ON CONFLICT DO NOTHING;
END;
$executor_run$;
