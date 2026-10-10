import { createStore, get } from 'idb-keyval';
import { botApi } from './bot';
import { entityId } from '@/utils/json';
import { useAuthStore } from '@/stores/auth';
import type { StartRunReq } from '@/types/api';
import type { BotRun } from '@/types/model';

export interface PendingRun {
  botId: string;
  request: StartRunReq;
  createdAt: number;
}
const store = createStore('aim-pending-runs-uuid', 'accounts');
const active = new Map<string, Promise<BotRun>>();

export async function loadPendingRuns(userId: string): Promise<PendingRun[]> {
  return (await get<PendingRun[]>(userId, store)) ?? [];
}

async function updatePending(userId: string, update: (items: PendingRun[]) => PendingRun[]): Promise<void> {
  await store('readwrite', (objectStore) => new Promise<void>((resolve, reject) => {
    const transaction = objectStore.transaction;
    transaction.oncomplete = () => resolve();
    transaction.onabort = () => reject(transaction.error ?? new Error('请求状态保存失败'));
    transaction.onerror = () => reject(transaction.error ?? new Error('请求状态保存失败'));
    const request = objectStore.get(userId);
    request.onsuccess = () => {
      try { objectStore.put(update(request.result ?? []), userId); }
      catch (error) { reject(error); transaction.abort(); }
    };
    request.onerror = () => reject(request.error);
  }));
}

export async function createPendingRun(userId: string, botId: string, data: Omit<StartRunReq, 'submission_key'>): Promise<PendingRun> {
  if (data.visibility === 'personal') throw new Error('个人工作不得写入浏览器持久缓存');
  entityId(userId, 'requester');
  entityId(botId, 'bot_id');
  entityId(data.conv_id, 'conv_id');
  const pending: PendingRun = { botId, request: structuredClone({ ...data, submission_key: crypto.randomUUID() }), createdAt: Date.now() };
  // Persist the exact body before HTTP. Reload and explicit retry reuse this UUID.
  await updatePending(userId, (items) => [...items, pending]);
  return pending;
}

export function retryPendingRun(userId: string, submissionKey: string): Promise<BotRun> {
  const revision = useAuthStore.getState().revision;
  const key = `${revision}:${userId}:${submissionKey}`;
  const running = active.get(key);
  if (running) return running;
  const work = (async () => {
    const pending = (await loadPendingRuns(userId)).find((item) => item.request.submission_key === submissionKey);
    if (!pending) throw new Error('待确认的请求不存在');
    if (useAuthStore.getState().user?.id !== userId || useAuthStore.getState().revision !== revision) throw new Error('账号已切换');
    const run = await botApi.startRun(pending.botId, pending.request);
    await updatePending(userId, (items) => items.filter((item) => item.request.submission_key !== submissionKey));
    return run;
  })().finally(() => active.delete(key));
  active.set(key, work);
  return work;
}
