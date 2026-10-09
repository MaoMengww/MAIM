-- Control facts and revocable authority are part of the ordered schema lineage.
DO $run_control$
BEGIN
 PERFORM pg_advisory_xact_lock(4278605, 1);
 IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version='008_run_control_membership') THEN RETURN; END IF;
 ALTER TABLE bot.bots ADD COLUMN authorization_version BIGINT NOT NULL DEFAULT 1;
 ALTER TABLE messaging.conv_bots ADD COLUMN authorization_version BIGINT NOT NULL DEFAULT 1;
 CREATE FUNCTION public.bot_authorization_epoch() RETURNS trigger LANGUAGE plpgsql AS $epoch$
 BEGIN
  IF OLD.status IS DISTINCT FROM NEW.status OR OLD.credentials_revoked_at IS DISTINCT FROM NEW.credentials_revoked_at THEN
   NEW.authorization_version := OLD.authorization_version + 1;
  END IF;
  RETURN NEW;
 END;
 $epoch$;
 CREATE TRIGGER bot_authorization_epoch BEFORE UPDATE ON bot.bots FOR EACH ROW EXECUTE FUNCTION public.bot_authorization_epoch();
 CREATE FUNCTION public.binding_authorization_epoch() RETURNS trigger LANGUAGE plpgsql AS $epoch$
 BEGIN
  IF OLD.revoked_at IS DISTINCT FROM NEW.revoked_at OR COALESCE(OLD.bot_settings::jsonb->'enabled','true'::jsonb) IS DISTINCT FROM COALESCE(NEW.bot_settings::jsonb->'enabled','true'::jsonb) THEN
   NEW.authorization_version := OLD.authorization_version + 1;
  END IF;
  RETURN NEW;
 END;
 $epoch$;
 CREATE TRIGGER binding_authorization_epoch BEFORE UPDATE ON messaging.conv_bots FOR EACH ROW EXECUTE FUNCTION public.binding_authorization_epoch();
 ALTER TABLE bot.runs
  ADD COLUMN controller_id UUID,
  ADD COLUMN controller_member_id UUID,
  ADD COLUMN requester_member_id UUID,
  ADD COLUMN control_user_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN stop_status TEXT NOT NULL DEFAULT 'none' CHECK(stop_status IN ('none','requested','confirmed','rejected','unknown')),
  ADD COLUMN control_memberships JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN stream_status TEXT NOT NULL DEFAULT 'open' CHECK(stream_status IN ('open','closed')),
  ADD COLUMN stop_reason TEXT NOT NULL DEFAULT '',
  ADD COLUMN stop_requested_at TIMESTAMPTZ,
  ADD COLUMN access_revoked BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN bot_authorization_version BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN binding_authorization_version BIGINT NOT NULL DEFAULT 1;
 UPDATE bot.runs r SET controller_id=requester_id,controller_member_id=(SELECT m.id FROM messaging.conv_members m WHERE m.conv_id=r.conv_id AND m.user_id=r.requester_id AND m.member_type='user' LIMIT 1);
 ALTER TABLE bot.runs ALTER COLUMN controller_id SET NOT NULL;
 UPDATE bot.runs SET stream_status='closed' WHERE status IN ('succeeded','failed','stopped');
 UPDATE bot.runs SET requester_member_id=controller_member_id;
 UPDATE bot.runs SET stop_status='confirmed' WHERE status='stopped';
 UPDATE bot.runs SET stop_status='requested',stop_requested_at=updated_at WHERE status='stopping';
 UPDATE bot.runs SET executor_capabilities=jsonb_set(jsonb_set(executor_capabilities,'{supports_stop}','true'::jsonb),'{supports_resume}','true'::jsonb) WHERE executor_type='builtin';
 ALTER TABLE bot.runs DROP CONSTRAINT runs_status_check;
 ALTER TABLE bot.runs ADD CONSTRAINT runs_status_check CHECK(status IN ('accepted','running','waiting_input','waiting_authorization','stopping','stopped','succeeded','failed'));
 ALTER TABLE bot.runs DROP CONSTRAINT runs_executor_status_check;
 ALTER TABLE bot.runs ADD CONSTRAINT runs_executor_status_check CHECK(executor_status IN ('pending','accepted','completed','stopped','stop_rejected'));
 INSERT INTO public.schema_migrations(version) VALUES('008_run_control_membership') ON CONFLICT DO NOTHING;
END;
$run_control$;
