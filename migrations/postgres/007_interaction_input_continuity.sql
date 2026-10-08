-- Explicit interaction continuity and append-only accepted input snapshots.
DO $interaction_input$
BEGIN
 PERFORM pg_advisory_xact_lock(4278605, 1);
 IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version='007_interaction_input_continuity') THEN RETURN; END IF;
 ALTER TABLE bot.interactions ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','closed')), ADD COLUMN closed_at TIMESTAMPTZ;
 ALTER TABLE bot.runs
  ADD COLUMN parent_run_id UUID REFERENCES bot.runs(id),
  ADD COLUMN interaction_status TEXT NOT NULL DEFAULT 'active' CHECK(interaction_status IN ('active','closed')),
  ADD COLUMN waiting_reason TEXT NOT NULL DEFAULT '',
  ADD COLUMN applied_input_version BIGINT NOT NULL DEFAULT 0 CHECK(applied_input_version >= 0 AND applied_input_version <= input_version);
 ALTER TABLE bot.run_inputs ADD COLUMN materials JSONB NOT NULL DEFAULT '[]', ADD COLUMN sources JSONB NOT NULL DEFAULT '[]';
 UPDATE bot.runs r SET materials=COALESCE((SELECT jsonb_agg(m.value || jsonb_build_object('requester_id',r.requester_id,'source_explicit',m.value->>'kind'<>'request_source') || CASE WHEN m.value->>'kind'='history' AND r.history_grant_id IS NOT NULL THEN jsonb_build_object('history_grant_id',r.history_grant_id) ELSE '{}'::jsonb END ORDER BY m.ordinality) FROM jsonb_array_elements(r.materials) WITH ORDINALITY m(value,ordinality)), '[]'::jsonb);
 UPDATE bot.run_inputs i SET materials=r.materials,sources=r.sources FROM bot.runs r WHERE i.run_id=r.id AND i.version=1;
 UPDATE bot.runs r SET applied_input_version=input_version WHERE r.executor_type='builtin' AND EXISTS (SELECT 1 FROM bot.run_trajectory t WHERE t.run_id=r.id);
 UPDATE bot.runs SET executor_capabilities=jsonb_set(executor_capabilities,'{supports_input}','true') WHERE executor_type='builtin';
 UPDATE bot.runs SET input_sources=jsonb_build_array(jsonb_build_object('version',1,'requester_id',requester_id,'source_message_id',source_message_id,'sender_type','user')) WHERE input_sources='[]'::jsonb;
 UPDATE bot.runs r SET input_sources=COALESCE((SELECT jsonb_agg(i.value || jsonb_build_object('sender_type','user','message_ids',COALESCE(i.value->'message_ids','[]'::jsonb),'file_ids',COALESCE(i.value->'file_ids','[]'::jsonb)) ORDER BY i.ordinality) FROM jsonb_array_elements(r.input_sources) WITH ORDINALITY i(value,ordinality)), '[]'::jsonb);
 -- Only unpublished delivery snapshots switch envelopes. Preserve their
 -- accepted upper bound and operation; completed transport facts stay intact.
 UPDATE bot.run_outbox o SET payload=(o.payload-'input') || jsonb_build_object('inputs',COALESCE((SELECT jsonb_agg(jsonb_build_object('version',i.version,'requester_id',i.requester_id,'sender_type','user','source_message_id',i.source_message_id,'content',i.content,'materials',i.materials,'sources',i.sources,'created_at',EXTRACT(EPOCH FROM i.created_at)::bigint) ORDER BY i.version) FROM bot.run_inputs i WHERE i.run_id=o.run_id AND i.version<=(o.payload->>'input_version')::bigint),'[]'::jsonb)) WHERE o.kind='deliver' AND o.completed_at IS NULL;
 UPDATE bot.run_outbox o SET payload=jsonb_set(o.payload,'{run}',(o.payload->'run') || jsonb_build_object('interaction_status','active','waiting_reason','') || CASE WHEN o.payload->'run'->>'executor_type'='builtin' THEN jsonb_build_object('executor_capabilities',jsonb_set(o.payload->'run'->'executor_capabilities','{supports_input}','true')) ELSE '{}'::jsonb END) WHERE o.kind IN ('deliver','updated') AND o.completed_at IS NULL;
 UPDATE bot.run_receipts SET receipt=receipt || jsonb_build_object('interaction_status','active','waiting_reason','');
 UPDATE bot.run_outbox o SET payload=jsonb_set(o.payload,'{run,input_sources}',COALESCE((SELECT jsonb_agg(s.value || jsonb_build_object('sender_type','user','message_ids',COALESCE(s.value->'message_ids','[]'::jsonb),'file_ids',COALESCE(s.value->'file_ids','[]'::jsonb)) ORDER BY s.ordinality) FROM jsonb_array_elements(o.payload->'run'->'input_sources') WITH ORDINALITY s(value,ordinality)),'[]'::jsonb)) WHERE o.kind IN ('deliver','updated') AND o.completed_at IS NULL;
 UPDATE bot.run_receipts r SET receipt=jsonb_set(r.receipt,'{input_sources}',COALESCE((SELECT jsonb_agg(s.value || jsonb_build_object('sender_type','user','message_ids',COALESCE(s.value->'message_ids','[]'::jsonb),'file_ids',COALESCE(s.value->'file_ids','[]'::jsonb)) ORDER BY s.ordinality) FROM jsonb_array_elements(r.receipt->'input_sources') WITH ORDINALITY s(value,ordinality)),'[]'::jsonb));
 CREATE INDEX idx_runs_interaction ON bot.runs(interaction_id,id);
 CREATE INDEX idx_runs_result_message ON bot.runs(message_id) WHERE message_id IS NOT NULL;
 INSERT INTO public.schema_migrations(version) VALUES('007_interaction_input_continuity') ON CONFLICT DO NOTHING;
END;
$interaction_input$;
