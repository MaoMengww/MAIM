import { createStore, get } from 'idb-keyval';
import type { Conversation, Message } from '@/types/model';

const snapshotStore = createStore('aim-user-sync', 'snapshots');

export interface UserSyncCache {
  position: string;
  messages: Record<string, Message[]>;
  conversations: Conversation[];
}

export async function loadUserSyncCache(userId: string): Promise<UserSyncCache | null> {
  return (await get<UserSyncCache>(userId, snapshotStore)) ?? null;
}

async function writeUserSyncCache(userId: string, update: (current: UserSyncCache) => UserSyncCache): Promise<void> {
  await snapshotStore('readwrite', (store) => new Promise<void>((resolve, reject) => {
    const transaction = store.transaction;
    transaction.oncomplete = () => resolve();
    transaction.onabort = () => reject(transaction.error ?? new Error('用户同步缓存事务已中止'));
    transaction.onerror = () => reject(transaction.error ?? new Error('用户同步缓存读写失败'));
    const request = store.get(userId);
    request.onerror = () => reject(request.error);
    request.onsuccess = () => {
      try {
        const current: UserSyncCache = request.result ?? { position: '0', messages: {}, conversations: [] };
        const write = store.put(update(current), userId);
        write.onerror = () => reject(write.error);
      } catch (error) {
        reject(error);
        transaction.abort();
      }
    };
  }));
}

export function commitUserSyncCache(userId: string, cache: UserSyncCache, expectedPosition: string): Promise<void> {
  return writeUserSyncCache(userId, (current) => {
    if (current.position !== expectedPosition) throw new Error('用户同步缓存位点已变更');
    return cache;
  });
}

// Live changes update the latest durable snapshot without advancing its position.
export function updateUserSyncCache(userId: string, apply: (messages: Record<string, Message[]>) => void): Promise<void> {
  return writeUserSyncCache(userId, (current) => {
    apply(current.messages);
    return current;
  });
}
