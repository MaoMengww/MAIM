import { get, set, del, createStore } from 'idb-keyval';
import type { Message } from '@/types/model';

// Ensure both object stores exist — fixes corrupted DB from early dev versions
// that left the DB at version 1 without stores (causing NotFoundError on tx).
{
  const req = indexedDB.open('aim-message-cache', 2);
  req.onupgradeneeded = () => {
    if (!req.result.objectStoreNames.contains('messages')) {
      req.result.createObjectStore('messages');
    }
    if (!req.result.objectStoreNames.contains('meta')) {
      req.result.createObjectStore('meta');
    }
  };
  req.onerror = () => {}; // silently ignore if unsupported
}

const msgStore = createStore('aim-message-cache', 'messages');
const metaStore = createStore('aim-message-cache', 'meta');

interface MessageCache {
  messages: Message[];
  maxSeq: number;
  updatedAt: number;
}

interface ConvMeta {
  maxSeq: number;
  preview?: string;
  updatedAt: number;
}

// ─── Per-conversation message cache ───

export async function loadMessageCache(convId: string): Promise<{ messages: Message[]; maxSeq: number } | null> {
  const cache = await get<MessageCache>(convId, msgStore);
  if (!cache) return null;
  return { messages: cache.messages, maxSeq: cache.maxSeq };
}

export async function saveMessageCache(convId: string, messages: Message[], maxSeq: number): Promise<void> {
  const cache: MessageCache = { messages, maxSeq, updatedAt: Date.now() };
  await set(convId, cache, msgStore);
  await set(convId, { maxSeq, updatedAt: Date.now() }, metaStore);
}

export async function appendMessageToCache(convId: string, msg: Message): Promise<void> {
  const cache = await get<MessageCache>(convId, msgStore);
  if (!cache) {
    await saveMessageCache(convId, [msg], msg.seq || 0);
    return;
  }
  if (cache.messages.some((m) => m.message_id === msg.message_id)) return;
  cache.messages.push(msg);
  if ((msg.seq || 0) > cache.maxSeq) cache.maxSeq = msg.seq || 0;
  cache.updatedAt = Date.now();
  await set(convId, cache, msgStore);
  await set(convId, { maxSeq: cache.maxSeq, updatedAt: Date.now() }, metaStore);
}

export async function updateMessageInCache(convId: string, msgId: number, updater: (msg: Message) => Message): Promise<void> {
  const cache = await get<MessageCache>(convId, msgStore);
  if (!cache) return;
  let changed = false;
  cache.messages = cache.messages.map((m) => {
    if (m.message_id !== msgId) return m;
    changed = true;
    return updater(m);
  });
  if (!changed) return;
  cache.updatedAt = Date.now();
  await set(convId, cache, msgStore);
}

export async function removeMessageFromCache(convId: string, msgId: number): Promise<void> {
  const cache = await get<MessageCache>(convId, msgStore);
  if (!cache) return;
  cache.messages = cache.messages.filter((m) => m.message_id !== msgId);
  cache.updatedAt = Date.now();
  await set(convId, cache, msgStore);
}

export async function clearMessageCache(convId: string): Promise<void> {
  await del(convId, msgStore);
  await del(convId, metaStore);
}

// ─── Conversation list cache ───

export async function saveConvListMetas(metas: Record<string, ConvMeta>): Promise<void> {
  await set('conv-list-metas', metas, metaStore);
}

export async function loadConvListMetas(): Promise<Record<string, ConvMeta> | undefined> {
  return get<Record<string, ConvMeta>>('conv-list-metas', metaStore);
}
