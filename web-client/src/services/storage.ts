import { createStore, get } from 'idb-keyval';
import type { Conversation, Message } from '@/types/model';
import { entityId, optionalEntityId, sequence, knowledgeSources } from '@/utils/json';

const snapshotStore = createStore('aim-user-sync-uuid', 'snapshots');

export interface UserSyncCache {
  position: number;
  messages: Record<string, Message[]>;
  conversations: Conversation[];
  deletedMessages: Record<string, string[]>;
}

function restoreUserSyncCache(value: unknown): UserSyncCache | null {
  try {
    if (!value || typeof value !== 'object') return null;
    const cache = value as UserSyncCache;
    sequence(cache.position, 'cache.position');
    if (!Array.isArray(cache.conversations) || !cache.messages || !cache.deletedMessages
      || typeof cache.messages !== 'object' || Array.isArray(cache.messages)
      || typeof cache.deletedMessages !== 'object' || Array.isArray(cache.deletedMessages)) return null;
    for (const conversation of cache.conversations) {
      entityId(conversation.id, 'cache.conversation.id');
      optionalEntityId(conversation.owner_id, 'cache.conversation.owner_id');
      optionalEntityId(conversation.peer_user_id, 'cache.conversation.peer_user_id');
      optionalEntityId(conversation.last_message_id, 'cache.conversation.last_message_id');
      sequence(conversation.max_seq, 'cache.conversation.max_seq');
      sequence(conversation.last_read_seq, 'cache.conversation.last_read_seq');
    }
    for (const [conversationId, messages] of Object.entries(cache.messages)) {
      entityId(conversationId, 'cache.messages.conversation_id');
      if (!Array.isArray(messages)) return null;
      for (const message of messages) {
        entityId(message.message_id, 'cache.message.message_id');
        if (entityId(message.conversation_id, 'cache.message.conversation_id') !== conversationId) return null;
        sequence(message.seq, 'cache.message.seq', true);
        if (!['user', 'bot', 'system'].includes(message.sender_type)) return null;
        if (message.sender_type === 'system') optionalEntityId(message.sender_id, 'cache.message.sender_id');
        else entityId(message.sender_id, 'cache.message.sender_id');
        optionalEntityId(message.binding_id, 'cache.message.binding_id');
        optionalEntityId(message.requester_id, 'cache.message.requester_id');
        if (![1, 2, 3, 4, 5, 6, 7, 8, 10].includes(message.type) || typeof message.sender_name !== 'string' || typeof message.sender_avatar !== 'string'
          || typeof message.raw_payload !== 'string') return null;
        if (message.status === 2 && (message.raw_payload !== ''
          || Object.values(message.content).some((body) => Object.keys(body).length > 0))) return null;
        if (message.raw_payload) knowledgeSources(JSON.parse(message.raw_payload).kb_sources);
        optionalEntityId(message.reply_to_id, 'cache.message.reply_to_id');
        optionalEntityId(message.client_msg_id, 'cache.message.client_msg_id');
        if (message.reply_to) {
          if (entityId(message.reply_to.message_id, 'cache.reply.message_id') !== message.reply_to_id) return null;
          optionalEntityId(message.reply_to.sender_id, 'cache.reply.sender_id');
        }
        for (const body of Object.values(message.content)) {
          for (const field of ['file_id', 'actor_id']) optionalEntityId(body[field], `cache.content.${field}`);
          for (const field of ['mention_user_ids', 'related_user_ids']) {
            if (body[field] !== undefined) {
              if (!Array.isArray(body[field])) return null;
              for (const id of body[field]) entityId(id, `cache.content.${field}`);
            }
          }
        }
      }
      const recalledIds = new Set(messages.filter((message) => message.status === 2).map((message) => message.message_id));
      if (messages.some((message) => message.reply_to && recalledIds.has(message.reply_to.message_id)
        && (!message.reply_to.deleted || message.reply_to.preview !== ''))) return null;
    }
    for (const [conversationId, messageIds] of Object.entries(cache.deletedMessages)) {
      entityId(conversationId, 'cache.deletedMessages.conversation_id');
      if (!Array.isArray(messageIds)) return null;
      for (const id of messageIds) entityId(id, 'cache.deletedMessages.message_id');
    }
    return cache;
  } catch {
    // Incompatible snapshots are rebuilt, never coerced into the new contract.
    return null;
  }
}

export async function loadUserSyncCache(userId: string): Promise<UserSyncCache | null> {
  entityId(userId, 'cache.user_id');
  return restoreUserSyncCache(await get<unknown>(userId, snapshotStore));
}

async function writeUserSyncCache(userId: string, update: (current: UserSyncCache) => UserSyncCache): Promise<void> {
  entityId(userId, 'cache.user_id');
  await snapshotStore('readwrite', (store) => new Promise<void>((resolve, reject) => {
    const transaction = store.transaction;
    transaction.oncomplete = () => resolve();
    transaction.onabort = () => reject(transaction.error ?? new Error('用户同步缓存事务已中止'));
    transaction.onerror = () => reject(transaction.error ?? new Error('用户同步缓存读写失败'));
    const request = store.get(userId);
    request.onerror = () => reject(request.error);
    request.onsuccess = () => {
      try {
        const current = restoreUserSyncCache(request.result) ?? { position: 0, messages: {}, conversations: [], deletedMessages: {} };
        const write = store.put(update(current), userId);
        write.onerror = () => reject(write.error);
      } catch (error) {
        reject(error);
        transaction.abort();
      }
    };
  }));
}

export function commitUserSyncCache(userId: string, cache: UserSyncCache, expectedPosition: number): Promise<void> {
  return writeUserSyncCache(userId, (current) => {
    if (current.position !== expectedPosition) throw new Error('用户同步缓存位点已变更');
    return cache;
  });
}

// Live changes update the latest durable snapshot without advancing its position.
export function updateUserSyncCache(userId: string, apply: (cache: UserSyncCache) => void): Promise<void> {
  return writeUserSyncCache(userId, (current) => {
    apply(current);
    return current;
  });
}
