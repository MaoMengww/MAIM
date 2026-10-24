import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { webcrypto } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

let submission, directory, outfile;
const stored = new Map();
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
const originalCrypto = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
before(async () => {
  Object.defineProperty(globalThis, 'crypto', { configurable: true, value: webcrypto });
  Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, value: {
    getItem: (key) => stored.get(key) ?? null,
    setItem: (key, value) => stored.set(key, value),
    removeItem: (key) => stored.delete(key),
  } });
  directory = await mkdtemp(join(tmpdir(), 'aim-skill-submission-'));
  outfile = join(directory, 'submission.mjs');
  await build({ entryPoints: ['src/utils/skillSubmission.ts'], bundle: true, platform: 'node', format: 'esm', outfile });
  submission = await import(pathToFileURL(outfile));
});
after(async () => {
  await rm(directory, { recursive: true, force: true });
  if (originalStorage) Object.defineProperty(globalThis, 'sessionStorage', originalStorage);
  else delete globalThis.sessionStorage;
  if (originalCrypto) Object.defineProperty(globalThis, 'crypto', originalCrypto);
  else delete globalThis.crypto;
});

test('one intent keeps its key across failures, concurrent attempts and module reload', async () => {
  const payload = { digest: 'frozen-content', confirmed: true };
  const [first, second] = await Promise.all([
    submission.skillSubmissionKey('user:bot:candidate:code-review', payload),
    submission.skillSubmissionKey('user:bot:candidate:code-review', payload),
  ]);
  assert.equal(first, second);
  const reloaded = await import(`${pathToFileURL(outfile)}?reload`);
  assert.equal(await reloaded.skillSubmissionKey('user:bot:candidate:code-review', payload), first);
  assert.notEqual(await submission.skillSubmissionKey('user:bot:candidate:publish', payload), first);
  assert.notEqual(await submission.skillSubmissionKey('user:bot:candidate:code-review', { ...payload, digest: 'changed-content' }), first);
});

test('known success consumes an approval intent; unknown failure retains its retry key', async () => {
  const scope = 'renewed-membership:approve';
  const payload = { digest: 'same-content' };
  const first = await submission.skillSubmissionKey(scope, payload);
  await assert.rejects(submission.submitSkillIntent(scope, payload, async (key) => {
    assert.equal(key, first);
    throw new Error('network result unknown');
  }), /unknown/);
  assert.equal(await submission.skillSubmissionKey(scope, payload), first);
  await submission.submitSkillIntent(scope, payload, async (key) => {
    assert.equal(key, first);
    return 'approved';
  });
  const renewed = await submission.skillSubmissionKey(scope, payload);
  assert.notEqual(renewed, first);
});

test('concurrent same-page submissions share one execution and one result', async () => {
  let calls = 0;
  let finish;
  const pending = new Promise((resolve) => { finish = resolve; });
  const execute = async () => { calls++; await pending; return 'known-success'; };
  const first = submission.submitSkillIntent('single-flight:evaluate', { digest: 'current' }, execute);
  const second = submission.submitSkillIntent('single-flight:evaluate', { digest: 'current' }, execute);
  finish();
  assert.deepEqual(await Promise.all([first, second]), ['known-success', 'known-success']);
  assert.equal(calls, 1);
});

test('session storage contains only request hashes and opaque keys, not source material', async () => {
  const sensitive = 'private CSV evidence and full source code';
  await submission.skillSubmissionKey('privacy:revise', { digest: 'current', instructions: sensitive });
  const serialized = JSON.stringify([...stored]);
  assert.equal(serialized.includes(sensitive), false);
});

test('unavailable storage still keeps retry identity within the live client', async () => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, get() { throw new Error('storage blocked'); } });
  try {
    const first = await submission.skillSubmissionKey('blocked:approve', { digest: 'current' });
    assert.equal(await submission.skillSubmissionKey('blocked:approve', { digest: 'current' }), first);
  } finally { Object.defineProperty(globalThis, 'sessionStorage', descriptor); }
});

test('CSV samples preserve exact bytes and reject over-budget files', async () => {
  const bytes = new Uint8Array([0xef, 0xbb, 0xbf, 0x61, 0x2c, 0x62, 0x0d, 0x0a]);
  assert.equal(await submission.skillFileBase64(new Blob([bytes])), Buffer.from(bytes).toString('base64'));
  await assert.rejects(submission.skillFileBase64(new Blob([new Uint8Array(65537)])), /64 KiB/);
});
