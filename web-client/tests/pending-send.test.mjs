import { test, before, after, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { build } from 'esbuild';
import 'fake-indexeddb/auto';

const uuid = (n) => `00000000-0000-7000-8000-${String(n).padStart(12, '0')}`;
const values = new Map();
globalThis.localStorage = { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: (key) => values.delete(key) };
let pending, auth, directory, server, serial = 0;
let requests = [], loseAck = false, holdAck;
const submissions = new Map();
before(async () => {
  server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const body = JSON.parse(Buffer.concat(chunks).toString());
    requests.push(body);
    const key = `${body.conversation_id}:${body.client_msg_id}`;
    if (!submissions.has(key)) submissions.set(key, { message_id: uuid(50000 + submissions.size), conv_id: body.conversation_id,
      from_user_id: auth.useAuthStore.getState().user.id, seq: 1, type: body.type, content: body.content,
      created_at: '123', reply_to_msg_id: body.reply_to_msg_id ?? null });
    if (loseAck) { loseAck = false; response.destroy(); return; }
    if (holdAck) await holdAck;
    response.setHeader('content-type', 'application/json');
    response.end(JSON.stringify({ code: 0, data: submissions.get(key) }));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  directory = await mkdtemp(join(tmpdir(), 'aim-pending-'));
  await symlink(join(process.cwd(), 'node_modules'), join(directory, 'node_modules'));
  await build({ stdin: { contents: "export * from './services/pendingSend'; export * from './stores/auth';", resolveDir: join(process.cwd(), 'src'), loader: 'ts' },
    bundle: true, platform: 'node', format: 'esm', outfile: join(directory, 'client.mjs'), packages: 'external',
    alias: { '@': join(process.cwd(), 'src') }, define: { 'import.meta.env.VITE_API_BASE': JSON.stringify(`http://127.0.0.1:${server.address().port}`) },
    banner: { js: `import { createRequire } from 'node:module'; const require = createRequire(${JSON.stringify(join(process.cwd(), 'package.json'))});` } });
  pending = auth = await import(pathToFileURL(join(directory, 'client.mjs')));
});
after(async () => { await new Promise((resolve) => server.close(resolve)); await rm(directory, { recursive: true, force: true }); });
beforeEach(() => { requests = []; loseAck = false; holdAck = undefined; auth.useAuthStore.setState({ user: { id: uuid(90000 + ++serial) }, revision: serial, token: 'test' }); });

test('lost confirmation leaves a durable original UUIDv4 key and retry resolves to the original message', async () => {
  const userId = auth.useAuthStore.getState().user.id;
  const draft = await pending.createPendingSend(userId, { conversation_id: uuid(101), type: 1, content: { text: 'original' }, reply_to_msg_id: uuid(12) });
  assert.match(draft.request.client_msg_id, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  loseAck = true;
  await assert.rejects(pending.retryPendingSend(userId, draft.request.client_msg_id));
  const restored = (await pending.loadPendingSends(userId))[0];
  assert.deepEqual(restored.request, draft.request);
  const result = await pending.retryPendingSend(userId, restored.request.client_msg_id);
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(result.message_id, submissions.get(`${uuid(101)}:${draft.request.client_msg_id}`).message_id);
  assert.notEqual(result.message_id, draft.request.client_msg_id);
  assert.deepEqual(await pending.loadPendingSends(userId), []);
});

test('failed local persistence sends nothing and concurrent retry does not duplicate an active request', async () => {
  const userId = auth.useAuthStore.getState().user.id;
  const put = IDBObjectStore.prototype.put;
  IDBObjectStore.prototype.put = function () { throw new DOMException('full', 'QuotaExceededError'); };
  try { await assert.rejects(pending.createPendingSend(userId, { conversation_id: uuid(101), type: 1, content: { text: 'not durable' } })); }
  finally { IDBObjectStore.prototype.put = put; }
  assert.equal(requests.length, 0);
  const draft = await pending.createPendingSend(userId, { conversation_id: uuid(101), type: 1, content: { text: 'durable' } });
  let release;
  holdAck = new Promise((resolve) => { release = resolve; });
  const first = pending.retryPendingSend(userId, draft.request.client_msg_id);
  const second = pending.retryPendingSend(userId, draft.request.client_msg_id);
  release();
  const [a, b] = await Promise.all([first, second]);
  assert.equal(a.message_id, b.message_id);
  assert.equal(requests.length, 1);
});

test('switching accounts while restoring a retry never sends another account draft', async () => {
  const userId = auth.useAuthStore.getState().user.id;
  const draft = await pending.createPendingSend(userId, { conversation_id: uuid(101), type: 1, content: { text: 'private draft' } });
  const retry = pending.retryPendingSend(userId, draft.request.client_msg_id);
  auth.useAuthStore.setState({ user: { id: uuid(999999) }, revision: 100000 });
  await assert.rejects(retry);
  assert.equal(requests.length, 0);
  assert.equal((await pending.loadPendingSends(userId))[0].request.content.text, 'private draft');
  assert.deepEqual(await pending.loadPendingSends(uuid(999999)), []);
});
