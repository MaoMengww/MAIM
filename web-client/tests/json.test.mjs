import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';

let protocol, directory;
before(async () => {
  directory = await mkdtemp(join(tmpdir(), 'aim-json-'));
  const outfile = join(directory, 'json.mjs');
  await build({ entryPoints: ['src/utils/json.ts'], bundle: true, platform: 'node', format: 'esm', outfile });
  protocol = await import(pathToFileURL(outfile));
});
after(async () => { await rm(directory, { recursive: true, force: true }); });

test('positions accept the final safe integer and reject text, fractions, negative and overflowing values', () => {
  assert.equal(protocol.sequence(9007199254740991, 'position'), 9007199254740991);
  assert.equal(protocol.sequence(0, 'position'), 0);
  for (const value of ['1', 1n, -1, 1.5, 9007199254740992, Infinity]) {
    assert.throws(() => protocol.sequence(value, 'position'));
  }
  assert.throws(() => protocol.sequence(0, 'message.seq', true));
});

test('entity references reject legacy numeric identities and explicit absence stays absent', () => {
  const id = '0198abcd-0000-7000-8000-000000000001';
  assert.equal(protocol.entityId(id, 'message_id'), id);
  assert.equal(protocol.optionalEntityId(null, 'sender_id'), undefined);
  assert.equal(protocol.optionalEntityId(undefined, 'sender_id'), undefined);
  for (const value of [1, '1', '0', '', '00000000-0000-0000-0000-000000000000', id.toUpperCase()]) {
    assert.throws(() => protocol.entityId(value, 'message_id'));
  }
});
