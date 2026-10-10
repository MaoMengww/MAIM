DO $work_source_validity$
BEGIN
    PERFORM pg_advisory_xact_lock(4278605, 1);
    IF EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '014_work_source_validity') THEN RETURN; END IF;
    IF NOT EXISTS (SELECT 1 FROM public.schema_migrations WHERE version = '013_working_compaction') THEN RAISE EXCEPTION '013_working_compaction must be applied before 014_work_source_validity'; END IF;
ALTER TABLE bot.runs ADD COLUMN source_changes JSONB NOT NULL DEFAULT '[]', ADD COLUMN context_notice TEXT NOT NULL DEFAULT '', ADD COLUMN source_revision BIGINT NOT NULL DEFAULT 1, ADD COLUMN branch_version BIGINT NOT NULL DEFAULT 1, ADD COLUMN provenance JSONB NOT NULL DEFAULT '[]';
ALTER TABLE bot.runs ADD COLUMN result_invalidated BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE bot.run_inputs ADD COLUMN accepted_edit_count INTEGER NOT NULL DEFAULT 0, ADD COLUMN provenance JSONB NOT NULL DEFAULT '[]', ADD COLUMN invalidated BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE bot.run_trajectory ADD COLUMN provenance JSONB NOT NULL DEFAULT '[]', ADD COLUMN invalidated BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE bot.run_model_calls ADD COLUMN source_revision BIGINT NOT NULL DEFAULT 1, ADD COLUMN checkpoint_id UUID, ADD COLUMN provenance JSONB NOT NULL DEFAULT '[]', ADD COLUMN invalidated BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE bot.runs r SET provenance=COALESCE((SELECT jsonb_agg(jsonb_build_object('message_id',m->>'message_id','accepted_edit_count',COALESCE((m->>'edit_count')::integer,0))) FROM jsonb_array_elements(r.materials) m WHERE COALESCE(m->>'message_id','')<>''),'[]'::jsonb);
UPDATE bot.run_inputs i SET provenance=COALESCE((SELECT jsonb_agg(jsonb_build_object('message_id',m->>'message_id','accepted_edit_count',COALESCE((m->>'edit_count')::integer,0))) FROM jsonb_array_elements(i.materials) m WHERE COALESCE(m->>'message_id','')<>''),'[]'::jsonb), accepted_edit_count=COALESCE((SELECT (m->>'edit_count')::integer FROM jsonb_array_elements(i.materials) m WHERE m->>'message_id'=i.source_message_id::text LIMIT 1),0);
-- Existing continuation copies also depend on their parent branch and all
-- accepted parent sources, not merely messages directly cited by this Run.
WITH RECURSIVE ancestry(run_id, parent_id, conv_id) AS (
 SELECT id, parent_run_id, conv_id FROM bot.runs WHERE parent_run_id IS NOT NULL
 UNION
 SELECT child.run_id, parent.parent_run_id, child.conv_id
 FROM ancestry child JOIN bot.runs parent ON parent.id = child.parent_id AND parent.conv_id = child.conv_id
 WHERE parent.parent_run_id IS NOT NULL
), inherited AS (
 SELECT ancestry.run_id,
  jsonb_agg(DISTINCT source.value) AS versions
 FROM ancestry JOIN bot.runs parent ON parent.id = ancestry.parent_id
 CROSS JOIN LATERAL jsonb_array_elements(parent.provenance || jsonb_build_array(
  jsonb_build_object('parent_run_id', parent.id, 'source_revision', parent.branch_version))
  || COALESCE((SELECT jsonb_agg(version.value) FROM bot.run_inputs input
     CROSS JOIN LATERAL jsonb_array_elements(input.provenance) AS version(value)
     WHERE input.run_id = parent.id), '[]'::jsonb)
  || CASE WHEN parent.message_id IS NOT NULL THEN jsonb_build_array(jsonb_build_object('message_id', parent.message_id, 'accepted_edit_count', 0)) ELSE '[]'::jsonb END
 ) AS source(value)
 GROUP BY ancestry.run_id
)
UPDATE bot.runs run SET provenance = run.provenance || inherited.versions,
 materials = COALESCE((SELECT jsonb_agg(CASE WHEN COALESCE(material.value->>'parent_run_id', '') <> ''
  THEN material.value || jsonb_build_object('provenance', inherited.versions) ELSE material.value END ORDER BY material.ordinality)
  FROM jsonb_array_elements(run.materials) WITH ORDINALITY AS material(value, ordinality)), '[]'::jsonb)
FROM inherited WHERE run.id = inherited.run_id;
UPDATE bot.run_inputs input SET provenance = input.provenance || run.provenance,
 materials = COALESCE((SELECT jsonb_agg(CASE WHEN COALESCE(material.value->>'parent_run_id', '') <> ''
  THEN material.value || jsonb_build_object('provenance', run.provenance) ELSE material.value END ORDER BY material.ordinality)
  FROM jsonb_array_elements(input.materials) WITH ORDINALITY AS material(value, ordinality)), '[]'::jsonb)
FROM bot.runs run WHERE input.run_id = run.id AND run.parent_run_id IS NOT NULL;
UPDATE bot.run_trajectory t SET provenance=r.provenance FROM bot.runs r WHERE r.id=t.run_id;
UPDATE bot.run_trajectory SET provenance='[]'::jsonb WHERE version=1 AND kind='message';
UPDATE bot.run_model_calls c SET provenance=r.provenance FROM bot.runs r WHERE r.id=c.run_id;

    INSERT INTO public.schema_migrations(version) VALUES ('014_work_source_validity') ON CONFLICT DO NOTHING;
END;
$work_source_validity$;
