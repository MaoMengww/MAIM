import { createStore, get } from 'idb-keyval';
import { msgApi } from './message';
import { entityId } from '@/utils/json';
import type { Message } from '@/types/model';
import type { SendMessageReq } from '@/types/api';
import { useAuthStore } from '@/stores/auth';

export interface PendingSend {
  request: SendMessageReq;
  createdAt: number;
  status: 'sending' | 'failed';
}
const store = createStore('aim-pending-sends-uuid', 'accounts');
const active = new Map<string, Promise<Message>>();

export async function loadPendingSends(userId: string): Promise<PendingSend[]> {
  return (await get<PendingSend[]>(userId, store)) ?? [];
}

async function updatePending(userId: string, update: (pending: PendingSend[]) => PendingSend[]): Promise<void> {
  await store('readwrite', (objectStore) => new Promise<void>((resolve, reject) => {
    const transaction = objectStore.transaction;
    transaction.oncomplete = () => resolve();
    transaction.onabort = () => reject(transaction.error ?? new Error('发送状态保存失败'));
    transaction.onerror = () => reject(transaction.error ?? new Error('发送状态保存失败'));
    const request = objectStore.get(userId);
    request.onsuccess = () => {
      try { objectStore.put(update(request.result ?? []), userId); }
      catch (error) { reject(error); transaction.abort(); }
    };
    request.onerror = () => reject(request.error);
  }));
}

export async function createPendingSend(userId: string, data: Omit<SendMessageReq, 'client_msg_id'>): Promise<PendingSend> {
  entityId(userId, 'sender');
  entityId(data.conversation_id, 'conversation_id');
  const pending: PendingSend = { request: structuredClone({ ...data, client_msg_id: crypto.randomUUID() }),
    createdAt: Date.now(), status: 'failed' };
  // No HTTP request is allowed until the original submission is durable.
  await updatePending(userId, (items) => [...items, pending]);
  return pending;
}

export function retryPendingSend(userId: string, clientMsgId: string): Promise<Message> {
  const key = `${userId}:${clientMsgId}`;
  const revision = useAuthStore.getState().revision;
  const running = active.get(key);
  if (running) return running;
  const run = (async () => {
    const pending = (await loadPendingSends(userId)).find((item) => item.request.client_msg_id === clientMsgId);
    if (!pending) throw new Error('待发送消息不存在');
    if (useAuthStore.getState().user?.id !== userId || useAuthStore.getState().revision !== revision) throw new Error('账号已切换');
    const message = await msgApi.send(pending.request);
    // On an ACK or persistence failure keep the original key for explicit retry.
    await updatePending(userId, (items) => items.filter((item) => item.request.client_msg_id !== clientMsgId));
    return message;
  })().finally(() => active.delete(key));
  active.set(key, run);
  return run;
}
