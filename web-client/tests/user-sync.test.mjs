import { test, before, after, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';
import 'fake-indexeddb/auto';
import { createStore, set as setStoredValue } from 'idb-keyval';

let engine, storage, server, directory;
let pages = [], positions = [], serial = 0;
const values = new Map();
const uuid = (n) => `00000000-0000-7000-8000-${String(n).padStart(12, '0')}`;
globalThis.localStorage = {
  getItem: (key) => values.get(key) ?? null,
  setItem: (key, value) => values.set(key, value),
  removeItem: (key) => values.delete(key),
};
const msg = (id, conv, seq, text = `message-${id}`) => ({
  message_id: typeof id === 'string' ? id : uuid(id), conversation_id: typeof conv === 'string' ? conv : uuid(conv), seq,
  from_user_id: uuid(2), type: 1, status: 1, text: { text },
});
const live = (message) => ({ message_id: message.message_id, seq: message.seq, conv_id: message.conversation_id, sender_id: message.from_user_id, msg_type: message.type, content: { text: message.text.text } });
const snapshot = (id, messages = []) => ({
  conversation: { id: typeof id === 'string' ? id : uuid(id), type: 2, name: `conversation-${id}`, max_seq: messages.at(-1)?.seq ?? 0 },
  messages,
});
const rebuild = (position, conversations) => ({ next_position: position, rebuild_required: true, rebuild_reason: 'new_device', conversations });
const change = (position, message) => ({ position, conversation_id: message.conversation_id, kind: 'message.new', message });
const snapshotStore = createStore('aim-user-sync-uuid', 'snapshots');

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
  await build({ entryPoints: ['src/services/storage.ts'], bundle: true, platform: 'node', format: 'esm', outfile: storageFile,
    packages: 'external', alias: { '@': join(process.cwd(), 'src') } });
  storage = await import(pathToFileURL(storageFile));
});
after(async () => { await new Promise((resolve) => server.close(resolve)); await rm(directory, { recursive: true, force: true }); });
beforeEach(() => { engine.reset(); pages = []; positions = []; });

async function start(page) {
  const account = uuid(900000 + ++serial);
  pages.push(page);
  engine.start(account);
  await engine.reSync();
  assert.equal(engine.getState(uuid(101)).error, null);
  return account;
}

test('one user position drains multiple conversations and survives reopening without realtime skipping a gap', async () => {
  const account = await start(rebuild(10, [snapshot(101), snapshot(202)]));
  engine.onWsMessage(uuid(101), live(msg(1003, 101, 3)));
  pages.push({ next_position: 11, has_more: true, changes: [change(11, msg(1001, 101, 1))] },
    { next_position: 13, changes: [change(12, msg(2001, 202, 1)), change(13, msg(1003, 101, 3))] });
  await engine.reSync();
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1001), uuid(1003)]);
  assert.deepEqual(engine.getState(uuid(202)).messages.map((m) => m.message_id), [uuid(2001)]);
  assert.deepEqual(positions, ['0', '10', '11']);
  engine.reset(); engine.start(account);
  pages.push({ next_position: 13 });
  await engine.reSync();
  assert.equal(positions.at(-1), '13');
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1001), uuid(1003)]);
});

test('a textual persisted position is discarded before hydration and replaced from the initial position', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1, 'obsolete cache')])]));
  const legacy = await storage.loadUserSyncCache(account);
  legacy.position = '10';
  await setStoredValue(account, legacy, snapshotStore);
  engine.reset();
  positions = [];
  engine.start(account);
  pages.push(rebuild(20, [snapshot(101, [msg(1002, 101, 2, 'rebuilt history')])]));
  await engine.reSync();
  assert.equal(engine.getState(uuid(101)).error, null);
  assert.deepEqual(positions, ['0']);
  assert.deepEqual(engine.getState(uuid(101)).messages.map((message) => message.message_id), [uuid(1002)]);
  assert.equal((await storage.loadUserSyncCache(account)).position, 20);
  engine.reset(); engine.start(account); pages.push({ next_position: 20 }); await engine.reSync();
  assert.equal(positions.at(-1), '20');
});

test('a textual cached message sequence cannot revive history under an otherwise numeric position', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1, 'obsolete cache')])]));
  const legacy = await storage.loadUserSyncCache(account);
  legacy.messages[uuid(101)][0].seq = '1';
  await setStoredValue(account, legacy, snapshotStore);
  engine.reset();
  positions = [];
  engine.start(account);
  pages.push(rebuild(20, [snapshot(202, [msg(2001, 202, 1, 'fresh history')])]));
  await engine.reSync();
  assert.equal(engine.getState(uuid(202)).error, null);
  assert.deepEqual(positions, ['0']);
  assert.deepEqual(engine.getState(uuid(101)).messages, []);
  assert.equal(engine.getState(uuid(202)).messages[0].content.text.text, 'fresh history');
});

test('legacy numeric reply and deletion references discard the entire persisted snapshot', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1)])]));
  const legacy = await storage.loadUserSyncCache(account);
  legacy.messages[uuid(101)][0].reply_to_id = 123;
  legacy.deletedMessages[uuid(101)] = ['456'];
  await setStoredValue(account, legacy, snapshotStore);
  assert.equal(await storage.loadUserSyncCache(account), null);
  engine.reset(); engine.start(account); pages.push(rebuild(20, [snapshot(101, [msg(1002, 101, 2)])])); await engine.reSync();
  assert.equal(positions.at(-1), '0');
  assert.deepEqual(engine.getState(uuid(101)).messages.map((message) => message.message_id), [uuid(1002)]);
  assert.deepEqual((await storage.loadUserSyncCache(account)).deletedMessages, {});
});

test('numeric knowledge source references invalidate a Bot snapshot and rebuild without restoring the stale source', async () => {
  const botMessage = { ...msg(1001, 101, 1), type: 9, text: undefined,
    bot: { bot_id: uuid(7), bot_name: 'source Bot', text: 'answer', is_streaming: false,
      raw_payload: JSON.stringify({ kb_sources: [{ type: 'rag', kb_id: uuid(8), doc_id: uuid(9), chunk_id: uuid(10), kb_name: 'KB', title: 'Doc', content: 'source' }] }) } };
  const account = await start(rebuild(10, [snapshot(101, [botMessage])]));
  const cache = await storage.loadUserSyncCache(account);
  const payload = JSON.parse(cache.messages[uuid(101)][0].content.bot.raw_payload);
  payload.kb_sources[0].chunk_id = 10;
  cache.messages[uuid(101)][0].content.bot.raw_payload = JSON.stringify(payload);
  await setStoredValue(account, cache, snapshotStore);
  assert.equal(await storage.loadUserSyncCache(account), null);
  engine.reset(); engine.start(account);
  pages.push(rebuild(20, [snapshot(101, [msg(1002, 101, 2, 'authoritative rebuild')])]));
  await engine.reSync();
  assert.equal(positions.at(-1), '0');
  assert.deepEqual(engine.getState(uuid(101)).messages.map((message) => message.message_id), [uuid(1002)]);
  assert.equal((await storage.loadUserSyncCache(account)).position, 20);
});

test('an invalid later entry rejects the entire page and leaves the durable position and cache unchanged', async () => {
  const account = await start(rebuild(10, [snapshot(101)]));
  pages.push({ next_position: 12, changes: [change(11, msg(1001, 101, 1)), { ...change(12, msg(1002, 101, 2)), conversation_id: uuid(202) }] });
  await engine.reSync();
  assert.notEqual(engine.getState(uuid(101)).error, null);
  assert.deepEqual(engine.getState(uuid(101)).messages, []);
  engine.reset(); engine.start(account);
  pages.push({ next_position: 11, changes: [change(11, msg(1001, 101, 1))] });
  await engine.reSync();
  assert.equal(positions.at(-1), '10');
  assert.equal(engine.getState(uuid(101)).messages[0].content.text.text, 'message-1001');
});

test('rebuild replaces removed conversations and histories while keeping a live message from its request window', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1)]), snapshot(202, [msg(2001, 202, 1)])]));
  pages.push(async () => {
    engine.onWsMessage(uuid(101), live(msg(1003, 101, 3)));
    return rebuild(20, [snapshot(101, [msg(1002, 101, 2)])]);
  });
  await engine.reSync();
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1002), uuid(1003)]);
  assert.deepEqual(engine.getState(uuid(202)).messages, []);
  assert.deepEqual(engine.getConversations().map((c) => c.id), [uuid(101)]);
  engine.reset(); engine.start(account); pages.push({ next_position: 20 }); await engine.reSync();
  assert.deepEqual(engine.getState(uuid(202)).messages, []);
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1002), uuid(1003)]);
});

test('failed IndexedDB write cannot publish messages or advance the next request position', async () => {
  await start(rebuild(10, [snapshot(101)]));
  const put = IDBObjectStore.prototype.put;
  IDBObjectStore.prototype.put = function () { throw new DOMException('disk full', 'QuotaExceededError'); };
  pages.push({ next_position: 11, changes: [change(11, msg(1001, 101, 1))] });
  try { await engine.reSync(); } finally { IDBObjectStore.prototype.put = put; }
  assert.notEqual(engine.getState(uuid(101)).error, null);
  assert.deepEqual(engine.getState(uuid(101)).messages, []);
  pages.push({ next_position: 11, changes: [change(11, msg(1001, 101, 1))] });
  await engine.reSync();
  assert.equal(positions.at(-1), '10');
  assert.equal(engine.getState(uuid(101)).messages[0].content.text.text, 'message-1001');
});

test('atomic snapshot commit rejects stale writers and separates accounts', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1)])]));
  const other = uuid(900000 + ++serial);
  await assert.rejects(storage.commitUserSyncCache(account, { position: 11, messages: {}, conversations: [], deletedMessages: {} }, 0));
  assert.equal((await storage.loadUserSyncCache(account)).position, 10);
  assert.equal((await storage.loadUserSyncCache(account)).messages[uuid(101)][0].content.text.text, 'message-1001');
  assert.equal(await storage.loadUserSyncCache(other), null);
});

test('rebuild does not resurrect previously received realtime history outside its authoritative recent window', async () => {
  await start(rebuild(10, [snapshot(101)]));
  engine.onWsMessage(uuid(101), live(msg(1001, 101, 1)));
  pages.push(rebuild(20, [snapshot(101, [msg(1002, 101, 2)])]));
  await engine.reSync();
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1002)]);
});

test('idle deletion survives reopening even when the inbox has no replay entry for it', async () => {
  const account = await start(rebuild(10, [snapshot(101, [msg(1001, 101, 1), msg(1002, 101, 2)])]));
  await engine.removeMessage(uuid(101), uuid(1001));
  engine.reset(); engine.start(account); pages.push({ next_position: 10 }); await engine.reSync();
  assert.equal(positions.at(-1), '10');
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1002)]);
});

test('a new conversation realtime message arriving during rebuild is retained across reopening', async () => {
  const account = await start(rebuild(10, [snapshot(101)]));
  pages.push(async () => {
    engine.onWsMessage(uuid(202), live(msg(2001, 202, 1)));
    return rebuild(20, [snapshot(101)]);
  });
  await engine.reSync();
  assert.equal(engine.getState(uuid(202)).messages[0].content.text.text, 'message-2001');
  engine.reset(); engine.start(account); pages.push({ next_position: 20 }); await engine.reSync();
  assert.equal(engine.getState(uuid(202)).messages[0].content.text.text, 'message-2001');
});

test('account deletion redacts cached reply summaries atomically and survives reopening', async () => {
  const original = msg(1001, 101, 1, 'private original');
  const reply = { ...msg(1002, 101, 2, 'visible reply'), reply_to_id: uuid(1001),
    reply_to: { message_id: uuid(1001), sender_id: uuid(2), sender_type: 'user', sender_name: 'Alice', type: 1, preview: 'private original', deleted: false } };
  const account = await start(rebuild(10, [snapshot(101, [original, reply])]));
  pages.push({ next_position: 11, changes: [{ position: 11, conversation_id: uuid(101), kind: 'message.deleted', message_id: uuid(1001) }] });
  await engine.reSync();
  const assertRedacted = () => {
    const remaining = engine.getState(uuid(101)).messages;
    assert.deepEqual(remaining.map((m) => m.message_id), [uuid(1002)]);
    assert.equal(remaining[0].content.text.text, 'visible reply');
    assert.equal(remaining[0].reply_to.deleted, true);
    assert.equal(remaining[0].reply_to.preview, '');
    assert.equal(remaining[0].reply_to.sender_id, undefined);
    assert.equal(remaining[0].reply_to.sender_name, '');
  };
  assertRedacted();
  engine.reset(); engine.start(account); pages.push({ next_position: 11 }); await engine.reSync();
  assertRedacted();
});

test('unsafe or legacy textual positions and sequences reject whole pages without advancing', async () => {
  const account = await start(rebuild(10, [snapshot(101)]));
  for (const value of ['11', -1, 1.5, 9007199254740992]) {
    pages.push({ next_position: value, changes: [] });
    await engine.reSync();
    assert.notEqual(engine.getState(uuid(101)).error, null);
    assert.equal((await storage.loadUserSyncCache(account)).position, 10);
  }
  pages.push({ next_position: 11, changes: [change(11, { ...msg(1001, 101, 1), seq: '1' })] });
  await engine.reSync();
  assert.deepEqual(engine.getState(uuid(101)).messages, []);
  assert.equal((await storage.loadUserSyncCache(account)).position, 10);
});

test('personal deletion tombstones reject delayed live echoes and survive rebuild with redacted replies', async () => {
  const original = msg(1001, 101, 1, 'hidden');
  const reply = { ...msg(1002, 101, 2), reply_to_id: uuid(1001),
    reply_to: { message_id: uuid(1001), preview: 'hidden', sender_id: uuid(2), type: 1 } };
  const account = await start(rebuild(10, [snapshot(101, [original, reply])]));
  await engine.removeMessage(uuid(101), uuid(1001));
  engine.onWsMessage(uuid(101), live(original));
  pages.push(rebuild(20, [snapshot(101, [original, reply])]));
  await engine.reSync();
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1002)]);
  assert.equal(engine.getState(uuid(101)).messages[0].reply_to.preview, '');
  engine.reset(); engine.start(account); pages.push({ next_position: 20 }); await engine.reSync();
  assert.deepEqual(engine.getState(uuid(101)).messages.map((m) => m.message_id), [uuid(1002)]);
});

test('an in-flight page from a previous account cannot commit into the newly selected account', async () => {
  const firstAccount = await start(rebuild(10, [snapshot(101)]));
  let release, arrived;
  const gate = new Promise((resolve) => { release = resolve; });
  const requested = new Promise((resolve) => { arrived = resolve; });
  pages.push(async () => { arrived(); await gate; return { next_position: 11, changes: [change(11, msg(1001, 101, 1, 'account A'))] }; });
  const oldSync = engine.reSync();
  await requested;
  const secondAccount = uuid(900000 + ++serial);
  engine.start(secondAccount);
  pages.push(rebuild(20, [snapshot(202, [msg(2001, 202, 1, 'account B')])]));
  await engine.reSync();
  release();
  await oldSync;
  assert.deepEqual(engine.getState(uuid(101)).messages, []);
  assert.equal(engine.getState(uuid(202)).messages[0].content.text.text, 'account B');
  assert.equal((await storage.loadUserSyncCache(firstAccount)).position, 10);
  assert.equal((await storage.loadUserSyncCache(secondAccount)).position, 20);
});
