import { loadUserSyncCache, commitUserSyncCache, updateUserSyncCache, type UserSyncCache } from './storage';
import { msgApi, normalizeRealtimeMessageContent, type InboxChange, type UserSyncPage } from './message';
import type { Message } from '@/types/model';

type Listener = (convId: string, snapshotChanged: boolean, removedConversations: readonly string[]) => void;
interface ConvState {
  messages: Message[];
  loading: boolean;
  error: string | null;
}
type Mutation = (cache: UserSyncCache) => void;

function mergeMessage(messages: Record<string, Message[]>, convId: string, msg: Message) {
  const list = messages[convId] ?? [];
  const index = list.findIndex((m) => m.message_id === msg.message_id);
  messages[convId] = index < 0 ? [...list, msg] : list.map((m, i) => i === index ? msg : m);
  messages[convId].sort((a, b) => a.seq - b.seq);
}

function removeMessageAndRedactReplies(messages: Record<string, Message[]>, convId: string, messageId: string) {
  messages[convId] = (messages[convId] ?? []).filter((msg) => msg.message_id !== messageId).map((msg) => {
    if (msg.reply_to_id !== messageId && msg.reply_to?.message_id !== messageId) return msg;
    return { ...msg, reply_to: { message_id: messageId, deleted: true, sender_id: undefined,
      sender_type: '', sender_name: '', type: 0, preview: '' } };
  });
}

function applyChange(cache: UserSyncCache, change: InboxChange) {
  const convId = change.conversation_id;
  if (change.kind === 'conversation.removed') {
    cache.conversations = cache.conversations.filter((conv) => conv.id !== convId);
    delete cache.messages[convId];
    return;
  }
  if (change.conversation) {
    const conversation = change.conversation;
    const index = cache.conversations.findIndex((conv) => conv.id === convId);
    cache.conversations = index < 0
      ? [...cache.conversations, conversation]
      : cache.conversations.map((conv, i) => i === index ? conversation : conv);
  }
  switch (change.kind) {
    case 'message.new':
    case 'message.edited':
    case 'message.recalled':
      if (!(cache.deletedMessages[convId] ?? []).includes(change.message.message_id)) mergeMessage(cache.messages, convId, change.message);
      break;
    case 'message.deleted':
      cache.deletedMessages[convId] = [...new Set([...(cache.deletedMessages[convId] ?? []), change.message_id])];
      cache.conversations = cache.conversations.map((conv) => conv.id === convId && conv.last_message_id === change.message_id
        ? { ...conv, last_message_id: undefined, last_message_preview: '' } : conv);
      removeMessageAndRedactReplies(cache.messages, convId, change.message_id);
      break;
  }
  for (const hiddenId of cache.deletedMessages[convId] ?? []) {
    removeMessageAndRedactReplies(cache.messages, convId, hiddenId);
    cache.conversations = cache.conversations.map((conv) => conv.id === convId && conv.last_message_id === hiddenId
      ? { ...conv, last_message_id: undefined, last_message_preview: '' } : conv);
  }
}

class MessageSyncEngine {
  private userId: string | null = null;
  private generation = 0;
  private cache: UserSyncCache = { position: 0, messages: {}, conversations: [], deletedMessages: {} };
  private loading = true;
  private error: string | null = null;
  private boot: Promise<void> = Promise.resolve();
  private syncing: Promise<void> | null = null;
  private syncRequested = false;
  private hydrated = false;
  private mutations: Mutation[] = [];
  private listeners = new Set<Listener>();
  private writes: Promise<void> = Promise.resolve();

  private enqueueWrite(operation: () => Promise<void>): Promise<void> {
    const result = this.writes.then(operation);
    // Each caller observes its own failure; subsequent writes must still be able to run.
    this.writes = result.catch(() => undefined);
    return result;
  }

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private notify(convId = '*', snapshotChanged = false, removedConversations: readonly string[] = []) {
    this.listeners.forEach((fn) => fn(convId, snapshotChanged, removedConversations));
  }

  getState(convId: string): ConvState {
    return { messages: this.cache.messages[convId] ?? [], loading: this.loading, error: this.error };
  }

  getConversations() {
    return this.cache.conversations;
  }

  start(userId: string): void {
    if (this.userId === userId) return;
    this.reset();
    this.userId = userId;
    const generation = this.generation;
    this.boot = this.writes.then(() => loadUserSyncCache(userId)).then((cache) => {
      if (generation !== this.generation) return;
      const restored: UserSyncCache = cache ?? { position: 0, conversations: [], messages: {}, deletedMessages: {} };
      this.mutations.forEach((apply) => apply(restored));
      this.cache = restored;
      this.hydrated = true;
      this.notify('*', true);
    });
    // The sync caller reports failures; observe early cache failures until WS opens.
    void this.boot.catch((error) => {
      if (generation !== this.generation) return;
      this.loading = false;
      this.error = error instanceof Error ? error.message : String(error);
      this.notify();
    });
  }

  reset(): void {
    this.generation++;
    this.userId = null;
    this.cache = { position: 0, messages: {}, conversations: [], deletedMessages: {} };
    this.loading = true;
    this.error = null;
    this.syncing = null;
    this.syncRequested = false;
    this.mutations = [];
    this.hydrated = false;
    this.notify();
  }

  reSync(): Promise<void> {
    if (!this.userId) return Promise.resolve();
    if (this.syncing) {
      this.syncRequested = true;
      return this.syncing;
    }
    const generation = this.generation;
    const userId = this.userId;
    this.loading = true;
    this.error = null;
    this.notify();
    this.syncRequested = false;
    const run = this.sync(userId, generation).catch((error) => {
      if (generation !== this.generation) return;
      this.error = error instanceof Error ? error.message : String(error);
    }).finally(() => {
      if (generation !== this.generation) return;
      this.syncing = null;
      this.loading = false;
      this.notify();
      if (this.syncRequested) void this.reSync();
    });
    this.syncing = run;
    return run;
  }

  private async sync(userId: string, generation: number): Promise<void> {
    await this.boot;
    if (generation !== this.generation) return;
    // A second tab may have committed since this runtime loaded its cache.
    const durable = await loadUserSyncCache(userId);
    if (generation !== this.generation) return;
    if (durable) {
      this.mutations.forEach((apply) => apply(durable));
      this.cache = durable;
    }
    let page: UserSyncPage;
    do {
      const position = this.cache.position;
      const requestMutationStart = this.mutations.length;
      page = await msgApi.sync({ position, limit: 50 });
      if (generation !== this.generation) return;
      const messages: Record<string, Message[]> = page.rebuild_required ? {} : { ...this.cache.messages };
      let conversations = this.cache.conversations;
      if (page.rebuild_required) {
        conversations = page.conversations.map((snapshot) => snapshot.conversation);
        page.conversations.forEach((snapshot) => {
          messages[snapshot.conversation.id] = snapshot.messages;
        });
      }
      const next: UserSyncCache = { position: page.next_position, messages, conversations,
        deletedMessages: structuredClone(this.cache.deletedMessages) };
      Object.entries(next.deletedMessages).forEach(([convId, ids]) => ids.forEach((messageId) => {
        removeMessageAndRedactReplies(next.messages, convId, messageId);
        next.conversations = next.conversations.map((conv) => conv.id === convId && conv.last_message_id === messageId
          ? { ...conv, last_message_id: undefined, last_message_preview: '' } : conv);
      }));
      if (!page.rebuild_required) page.changes.forEach((change) => applyChange(next, change));
      const removedConversations = new Set<string>();
      page.changes.forEach((change) => {
        if (change.kind === 'conversation.removed') removedConversations.add(change.conversation_id);
        else if (change.conversation) removedConversations.delete(change.conversation_id);
      });
      if (page.rebuild_required) {
        const rebuiltIds = new Set(next.conversations.map((conversation) => conversation.id));
        this.cache.conversations.forEach((conversation) => {
          if (!rebuiltIds.has(conversation.id)) removedConversations.add(conversation.id);
        });
      }
      // New messages arriving while the request was in flight are not in its snapshot.
      // Rebuild replaces old state but retains live changes arriving after its request began.
      const replayLiveChanges = (start: number) => {
        this.mutations.slice(start).forEach((apply) => apply(next));
        // Live echoes cannot revive messages or conversations removed by this page.
        page.changes.forEach((change) => {
          if (change.kind === 'message.deleted'
            || (change.kind === 'conversation.removed' && removedConversations.has(change.conversation_id))) applyChange(next, change);
        });
      };
      replayLiveChanges(page.rebuild_required ? requestMutationStart : 0);
      const consumed = this.mutations.length;
      await this.enqueueWrite(async () => {
        if (generation === this.generation) await commitUserSyncCache(userId, next, position);
      });
      if (generation !== this.generation) return;
      this.mutations.splice(0, consumed);
      replayLiveChanges(0);
      this.cache = next;
      this.notify('*', page.rebuild_required || page.changes.length > 0, [...removedConversations]);
    } while (page.has_more);
  }

  private mutate(convId: string, apply: Mutation): Promise<void> {
    if (!this.userId) return Promise.resolve();
    if (!this.hydrated || this.syncing) this.mutations.push(apply);
    apply(this.cache);
    this.notify(convId);
    const userId = this.userId;
    const generation = this.generation;
    const boot = this.boot;
    return this.enqueueWrite(async () => {
      await boot;
      if (generation !== this.generation) return;
      await updateUserSyncCache(userId, apply);
    }).catch((error) => {
      if (generation !== this.generation) return;
      this.error = error instanceof Error ? error.message : String(error);
      this.notify();
    });
  }

  onWsMessage(convId: string, rawPayload: unknown): void {
    const msg = normalizeRealtimeMessageContent(rawPayload);
    if (!msg?.message_id || msg.conversation_id !== convId) return;
    void this.addMessage(convId, msg);
  }

  addMessage(convId: string, msg: Message): Promise<void> {
    if (!msg?.message_id) return Promise.resolve();
    return this.mutate(convId, (cache) => {
      if ((cache.deletedMessages[convId] ?? []).includes(msg.message_id)
        || cache.messages[convId]?.some((m) => m.message_id === msg.message_id)) return;
      mergeMessage(cache.messages, convId, msg);
      for (const hiddenId of cache.deletedMessages[convId] ?? []) removeMessageAndRedactReplies(cache.messages, convId, hiddenId);
    });
  }

  updateMessage(convId: string, messageId: string, updater: (msg: Message) => Message): Promise<void> {
    return this.mutate(convId, (cache) => {
      cache.messages[convId] = (cache.messages[convId] ?? []).map((m) => m.message_id === messageId ? updater(m) : m);
    });
  }

  removeMessage(convId: string, messageId: string): Promise<void> {
    return this.mutate(convId, (cache) => {
      applyChange(cache, { position: cache.position, conversation_id: convId, kind: 'message.deleted', message_id: messageId });
    });
  }
}

export const messageSync = new MessageSyncEngine();
