import { test, before, after, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';
import 'fake-indexeddb/auto';

let engine, storage, server, directory;
let pages = [], positions = [], serial = 0;
const values = new Map();
globalThis.localStorage = {
  getItem: (key) => values.get(key) ?? null,
  setItem: (key, value) => values.set(key, value),
  removeItem: (key) => values.delete(key),
};
const msg = (id, conv, seq, text = `message-${id}`) => ({
  message_id: String(id), conversation_id: String(conv), seq: String(seq),
  from_user_id: '2', type: 1, status: 1, text: { text },
});
const snapshot = (id, messages = []) => ({
  conversation: { id: String(id), type: 2, name: `conversation-${id}`, max_seq: String(messages.at(-1)?.seq ?? 0) },
  messages,
});
const rebuild = (position, conversations) => ({ next_position: String(position), rebuild_required: true, rebuild_reason: 'new_device', conversations });
const change = (position, message) => ({ position: String(position), conversation_id: message.conversation_id, kind: 'message.new', message });

before(async () => {
  server = createServer(async (request, response) => {
    const url = new URL(request.url, 'http://localhost');
    positions.push(url.searchParams.get('position'));
    const page = pages.shift();
    response.setHeader('content-type', 'application/json');
    if (!page) { response.statusCode = 500; response.end(JSON.stringify({ code: 1006 })); return; }
    response.end(typeof page === 'string' ? page : JSON.stringify({ code: 0, data: typeof page === 'function' ? await page() : page }));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  directory = await mkdtemp(join(tmpdir(), 'aim-user-sync-'));
  const outfile = join(directory, 'client.mjs');
  await build({ entryPoints: ['src/services/messageSync.ts'], bundle: true, platform: 'node', format: 'esm', outfile,
    packages: 'external', alias: { '@': join(process.cwd(), 'src') },
    define: { 'import.meta.env.VITE_API_BASE': JSON.stringify(`http://127.0.0.1:${server.address().port}`) },
    banner: { js: `import { createRequire } from 'node:module'; const require = createRequire(${JSON.stringify(join(process.cwd(), 'package.json'))});` },
  });
  // Bundled dependencies resolve against the project's node_modules, not /tmp.
  const { symlink } = await import('node:fs/promises');
  await symlink(join(process.cwd(), 'node_modules'), join(directory, 'node_modules'));
  engine = (await import(pathToFileURL(outfile))).messageSync;
  const storageFile = join(directory, 'storage.mjs');
  await build({ entryPoints: ['src/services/storage.ts'], bundle: true, platform: 'node', format: 'esm', outfile: storageFile, packages: 'external' });
  storage = await import(pathToFileURL(storageFile));
});
after(async () => { await new Promise((resolve) => server.close(resolve)); await rm(directory, { recursive: true, force: true }); });
beforeEach(() => { engine.reset(); pages = []; positions = []; });

async function start(page) {
  const account = `account-${++serial}`;
  pages.push(page);
  engine.start(account);
  await engine.reSync();
  assert.equal(engine.getState('101').error, null);
  return account;
}

test('one user position drains multiple conversations and survives reopening without realtime skipping a gap', async () => {
  const account = await start(rebuild(10, [snapshot(101), snapshot(202)]));
  engine.onWsMessage('101', msg(1003, 101, 3));
  pages.push({ next_position: '11', has_more: true, changes: [change(11, msg(1001, 101, 1))] },
    { next_position: '13', changes: [change(12, msg(2001, 202, 1)), change(13, msg(1003, 101, 3))] });
  await engine.reSync();
  assert.deepEqual(engine.getState('101').messages.map((m) => String(m.message_id)), ['1001', '1003']);
  assert.deepEqual(engine.getState('202').messages.map((m) => String(m.message_id)), ['2001']);
  assert.deepEqual(positions, ['0', '10', '11']);
  engine.reset(); engine.start(account);
  pages.push({ next_position: '13' });
  await engine.reSync();
  assert.equal(positions.at(-1), '13');
  assert.deepEqual(engine.getState('101').messages.map((m) => String(m.message_id)), ['1001', '1003']);
});

test('an invalid later entry rejects the entire page and leaves the durable position and cache unchanged', async () => {
  const account = await start(rebuild(10, [snapshot(101)]));
  pages.push({ next_position: '12', changes: [change(11, msg(1001, 101, 1)), { ...change(12, msg(1002, 101, 2)), conversation_id: '202' }] });
  await engine.reSync();
  assert.notEqual(engine.getState('101').error, null);
  assert.deepEqual(engine.getState('101').messages, []);
  engine.reset(); engine.start(account);
  pages.push({ next_position: '11', changes: [change(11, msg(1001, 101, 1))] });
  await engine.reSync();
  assert.equal(positions.at(-1), '10');
  assert.equal(engine.getState('101').messages[0].content.text.text, 'message-1001');
});

test('rebuild replaces removed conversations and histories while keeping a live message from its request window', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1)]), snapshot(202, [msg(2001, 202, 1)])]));
  pages.push(async () => {
    engine.onWsMessage('101', msg(1003, 101, 3));
    return rebuild(20, [snapshot(101, [msg(1002, 101, 2)])]);
  });
  await engine.reSync();
  assert.deepEqual(engine.getState('101').messages.map((m) => String(m.message_id)), ['1002', '1003']);
  assert.deepEqual(engine.getState('202').messages, []);
  assert.deepEqual(engine.getConversations().map((c) => String(c.id)), ['101']);
  engine.reset(); engine.start(account); pages.push({ next_position: '20' }); await engine.reSync();
  assert.deepEqual(engine.getState('202').messages, []);
  assert.deepEqual(engine.getState('101').messages.map((m) => String(m.message_id)), ['1002', '1003']);
});

test('failed IndexedDB write cannot publish messages or advance the next request position', async () => {
  await start(rebuild(10, [snapshot(101)]));
  const put = IDBObjectStore.prototype.put;
  IDBObjectStore.prototype.put = function () { throw new DOMException('disk full', 'QuotaExceededError'); };
  pages.push({ next_position: '11', changes: [change(11, msg(1001, 101, 1))] });
  try { await engine.reSync(); } finally { IDBObjectStore.prototype.put = put; }
  assert.notEqual(engine.getState('101').error, null);
  assert.deepEqual(engine.getState('101').messages, []);
  pages.push({ next_position: '11', changes: [change(11, msg(1001, 101, 1))] });
  await engine.reSync();
  assert.equal(positions.at(-1), '10');
  assert.equal(engine.getState('101').messages[0].content.text.text, 'message-1001');
});

test('atomic snapshot commit rejects stale writers and separates accounts', async () => {
  const account = `account-${++serial}`;
  const other = `account-${++serial}`;
  await storage.commitUserSyncCache(account, { position: '10', messages: { '101': [msg(1001, 101, 1)] }, conversations: [] }, '0');
  await assert.rejects(storage.commitUserSyncCache(account, { position: '11', messages: {}, conversations: [] }, '0'));
  assert.equal((await storage.loadUserSyncCache(account)).position, '10');
  assert.equal((await storage.loadUserSyncCache(account)).messages['101'][0].text.text, 'message-1001');
  assert.equal(await storage.loadUserSyncCache(other), null);
});

test('rebuild does not resurrect previously received realtime history outside its authoritative recent window', async () => {
  await start(rebuild(10, [snapshot(101)]));
  engine.onWsMessage('101', msg(1001, 101, 1));
  pages.push(rebuild(20, [snapshot(101, [msg(1002, 101, 2)])]));
  await engine.reSync();
  assert.deepEqual(engine.getState('101').messages.map((m) => String(m.message_id)), ['1002']);
});

test('neighboring snowflake message and conversation IDs remain distinct through sync, realtime and persistence', async () => {
  const convA = '2107179204238905344', convB = '2107179204238905345';
  const idA = '2107179443465228288', idB = '2107179443465228289';
  const account = await start(rebuild('9007199254740993', [snapshot(convA), snapshot(convB)]));
  engine.onWsMessage(convA, msg(idA, convA, 1, 'exact-A'));
  pages.push({ next_position: '9007199254740995', changes: [
    change('9007199254740994', msg(idA, convA, 1, 'exact-A')),
    change('9007199254740995', msg(idB, convB, 1, 'exact-B')),
  ] });
  await engine.reSync();
  assert.equal(positions.at(-1), '9007199254740993');
  engine.reset(); engine.start(account); pages.push({ next_position: '9007199254740995' }); await engine.reSync();
  assert.deepEqual(engine.getState(convA).messages.map((m) => String(m.message_id)), [idA]);
  assert.deepEqual(engine.getState(convB).messages.map((m) => String(m.message_id)), [idB]);
  engine.removeMessage(convA, idA);
  assert.deepEqual(engine.getState(convA).messages, []);
  assert.equal(engine.getState(convB).messages[0].content.text.text, 'exact-B');
});

test('bare int64 JSON tokens preserve exact IDs and positions across the HTTP boundary', async () => {
  const account = `account-${++serial}`;
  engine.start(account);
  pages.push('{"code":0,"data":{"next_position":9007199254740993,"rebuild_required":true,"rebuild_reason":"new_device","conversations":[{"conversation":{"id":2107179204238905344,"type":2,"max_seq":2},"messages":[{"message_id":2107179443465228288,"conversation_id":2107179204238905344,"seq":1,"type":1,"text":{"text":"first"}},{"message_id":2107179443465228289,"conversation_id":2107179204238905344,"seq":2,"type":1,"text":{"text":"second"}}]}]}}');
  await engine.reSync();
  assert.equal(engine.getState('2107179204238905344').error, null);
  assert.deepEqual(engine.getState('2107179204238905344').messages.map((m) => String(m.message_id)), ['2107179443465228288', '2107179443465228289']);
  pages.push({ next_position: '9007199254740993' });
  await engine.reSync();
  assert.equal(positions.at(-1), '9007199254740993');
});

test('idle deletion survives reopening even when the inbox has no replay entry for it', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1), msg(1002, 101, 2)])]));
  await engine.removeMessage('101', '1001');
  engine.reset(); engine.start(account); pages.push({ next_position: '10' }); await engine.reSync();
  assert.equal(positions.at(-1), '10');
  assert.deepEqual(engine.getState('101').messages.map((m) => String(m.message_id)), ['1002']);
});

test('a new conversation realtime message arriving during rebuild is retained across reopening', async () => {
  const account = await start(rebuild(10, [snapshot(101)]));
  pages.push(async () => {
    engine.onWsMessage('202', msg(2001, 202, 1));
    return rebuild(20, [snapshot(101)]);
  });
  await engine.reSync();
  assert.equal(engine.getState('202').messages[0].content.text.text, 'message-2001');
  engine.reset(); engine.start(account); pages.push({ next_position: '20' }); await engine.reSync();
  assert.equal(engine.getState('202').messages[0].content.text.text, 'message-2001');
});
