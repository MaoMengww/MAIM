import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

let candidateApi, directory;
before(async () => {
  directory = await mkdtemp(join(tmpdir(), 'aim-candidate-reintegration-'));
  const outfile = join(directory, 'candidate.mjs');
  await build({ entryPoints: ['src/services/skillCandidate.ts'], bundle: true, platform: 'node', format: 'esm', outfile,
    plugins: [{ name: 'unused-http-boundary', setup(builder) {
      builder.onResolve({ filter: /^\.\/client$/ }, () => ({ path: 'client', namespace: 'http-boundary' }));
      builder.onLoad({ filter: /.*/, namespace: 'http-boundary' }, () => ({ contents: 'export default {}; export const unwrap = value => value;', loader: 'js' }));
    } }],
  });
  candidateApi = await import(pathToFileURL(outfile));
});
after(async () => { await rm(directory, { recursive: true, force: true }); });
const id = '01900000-0000-7000-8000-000000000001';
const currentID = '01900000-0000-7000-8000-000000000002';
const packageBody = { instructions: 'Preserve zero totals.', resources: { 'references/a.md': 'zero totals' }, scripts: {}, image_digest: '', entry_point: '' };
const fixture = () => ({ id, skill_id: id, source_run_id: id, publisher_id: id, scope_key: `conversation:${id}`, status: 'approved', digest: 'candidate-digest', expires_at: Math.floor(Date.now() / 1000) + 600,
  resources: {}, scripts: {}, can_maintain: true, can_view_materials: true, can_review_code: true, can_publish: true, code_review_required: false,
  baseline_stale: true, current_version_id: currentID, current_content: packageBody,
});

test('authorized comparison preserves exact current version and complete package without merging candidate content', () => {
  const candidate = candidateApi.normalizeGovernedCandidate({ ...fixture(), instructions: 'Trim whitespace.' });
  assert.equal(candidate.baseline_stale, true);
  assert.equal(candidate.current_version_id, currentID);
  assert.deepEqual(candidate.current_content, packageBody);
  assert.equal(candidate.instructions, 'Trim whitespace.');
  assert.deepEqual(candidate.resources, {});
});

test('a new unpublished skill has an explicit non-stale empty baseline, not a malformed response', () => {
  const raw = fixture();
  delete raw.current_version_id;
  delete raw.current_content;
  raw.baseline_stale = false;
  const candidate = candidateApi.normalizeGovernedCandidate(raw);
  assert.equal(candidate.current_version_id, null);
  assert.equal(candidate.baseline_stale, false);
  raw.baseline_stale = true;
  assert.throws(() => candidateApi.normalizeGovernedCandidate(raw), /当前基线身份缺失/);
});

test('expiry and authorization loss remove comparison bodies and current target identity', () => {
  for (const patch of [{ expires_at: Math.floor(Date.now() / 1000) - 1 }, { can_view_materials: false }]) {
    const candidate = candidateApi.normalizeGovernedCandidate({ ...fixture(), ...patch });
    assert.equal(candidate.current_content, undefined);
    assert.equal(candidate.baseline_content, undefined);
    assert.equal(candidate.current_version_id, undefined);
    assert.equal(candidate.baseline_stale, undefined);
    assert.equal(candidate.instructions, '');
  }
});

test('archive candidates need no fabricated source Run and retain the same explicit governance permissions', () => {
  const raw = { ...fixture(), origin: 'archive', scope_key: 'bot', source_run_id: undefined, status: 'pending_validation',
    code_review_required: true, scripts: { 'worker.py': 'review me before execution' }, code_review_digest: '' };
  const candidate = candidateApi.normalizeGovernedCandidate(raw);
  assert.equal(candidate.origin, 'archive');
  assert.equal(candidate.source_run_id, '');
  assert.equal(candidate.code_review_required, true);
  assert.equal(candidate.code_review_digest, '');
  assert.equal(candidate.status, 'pending_validation');
  assert.equal(candidate.evaluation, undefined);
  assert.equal(candidate.archive_baseline_stale, false);
  const standalone = { ...raw, current_version_id: undefined, current_content: undefined, baseline_stale: undefined, archive_baseline_stale: true };
  assert.equal(candidateApi.normalizeGovernedCandidate(standalone).archive_baseline_stale, true);
  assert.throws(() => candidateApi.normalizeGovernedCandidate({ ...raw, can_review_code: undefined }), /权限字段缺失/);
  assert.throws(() => candidateApi.normalizeGovernedCandidate({ ...raw, origin: undefined }), /source_run_id/);
});

test('expired archive metadata never retains code, image, samples or a publication capability', () => {
  const candidate = candidateApi.normalizeGovernedCandidate({ ...fixture(), origin: 'archive', source_run_id: undefined,
    expires_at: Math.floor(Date.now() / 1000) - 1, scripts: { 'worker.py': 'sensitive code' },
    input_b64: 'cHJpdmF0ZQ==', expected_output_b64: 'cHJpdmF0ZQ==', image_digest: 'sha256:private' });
  assert.equal(candidate.origin, 'archive');
  assert.deepEqual(candidate.scripts, {});
  assert.equal(candidate.image_digest, '');
  assert.equal(candidate.input_b64, '');
  assert.equal(candidate.expected_output_b64, '');
  assert.equal(candidate.can_publish, false);
  assert.equal(candidate.can_review_code, false);
});
