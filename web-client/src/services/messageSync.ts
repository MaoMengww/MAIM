import { loadMessageCache, saveMessageCache, appendMessageToCache, updateMessageInCache, removeMessageFromCache } from './storage';
import { msgApi, normalizeRealtimeMessageContent } from './message';
import type { Message } from '@/types/model';

type Listener = (convId: string) => void;

interface ConvState {
  messages: Message[];
  maxSeq: number;
  initialized: boolean;
  loading: boolean;
  error: string | null;
}

class MessageSyncEngine {
  private states = new Map<string, ConvState>();
  private listeners = new Set<Listener>();

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private notify(convId: string) {
    this.listeners.forEach((fn) => {
      try {
        fn(convId);
      } catch { /* guard */ }
    });
  }

  getState(convId: string): ConvState | undefined {
    return this.states.get(convId);
  }

  isInitialized(convId: string): boolean {
    return this.states.get(convId)?.initialized ?? false;
  }

  isLoading(convId: string): boolean {
    return this.states.get(convId)?.loading ?? false;
  }

  getMessages(convId: string): Message[] {
    return this.states.get(convId)?.messages ?? [];
  }

  getMaxSeq(convId: string): number {
    return this.states.get(convId)?.maxSeq ?? 0;
  }

  /**
   * 加载会话消息：IndexedDB → 增量同步 → 合并排序 → 持久化
   */
  async init(convId: string): Promise<void> {
    const existing = this.states.get(convId);
    if (existing?.initialized && !existing.loading) return;

    this.states.set(convId, {
      messages: [],
      maxSeq: 0,
      initialized: false,
      loading: true,
      error: null,
    });
    this.notify(convId);

    try {
      // 1. 从 IndexedDB 加载本地缓存
      const cache = await loadMessageCache(convId);
      const cached = cache?.messages ?? [];
      const cachedMaxSeq = cache?.maxSeq ?? 0;

      // 缓存有数据时立即展示，不等同步完成
      if (cached.length > 0) {
        // 合并 init 期间 WS 已推送的消息
        const inflight = this.states.get(convId)?.messages ?? [];
        const cachedIds = new Set(cached.map((m) => m.message_id));
        const merged = [...cached];
        for (const msg of inflight) {
          if (!cachedIds.has(msg.message_id)) {
            merged.push(msg);
          }
        }
        this.states.set(convId, {
          messages: merged,
          maxSeq: cachedMaxSeq,
          initialized: false,
          loading: true,
          error: null,
        });
        this.notify(convId);
      }

      // 2. 增量同步（首次全量拉取）
      const result = await msgApi.sync({
        conversation_id: convId as any,
        from_seq: cachedMaxSeq > 0 ? cachedMaxSeq : undefined,
      });

      // 3. 合并
      // init 期间 WS 可能已推送消息到当前 state，合并时需保留
      const inFlightMsgs = this.states.get(convId)?.messages ?? [];

      let merged: Message[];
      if (cached.length > 0) {
        const existingIds = new Set(cached.map((m) => m.message_id));
        merged = [...cached];
        for (const msg of result.messages) {
          if (!existingIds.has(msg.message_id)) {
            merged.push(msg);
          }
        }
      } else {
        merged = result.messages;
      }

      // init 期间 WS 推送的消息（尚未在 merged 中的）
      const mergedIds = new Set(merged.map((m) => m.message_id));
      for (const msg of inFlightMsgs) {
        if (!mergedIds.has(msg.message_id)) {
          merged.push(msg);
        }
      }

      // 4. 按 seq 排序
      merged.sort((a, b) => (a.seq || 0) - (b.seq || 0));

      // 5. 更新状态
      const newMaxSeq = Math.max(result.max_seq || 0, cachedMaxSeq);
      this.states.set(convId, {
        messages: merged,
        maxSeq: newMaxSeq,
        initialized: true,
        loading: false,
        error: null,
      });

      // 6. 异步持久化到 IndexedDB
      saveMessageCache(convId, merged, newMaxSeq);
      this.notify(convId);
    } catch (err: any) {
      // 取当前状态（可能已加载缓存），保留已有消息
      const current = this.states.get(convId)!;
      this.states.set(convId, {
        ...current,
        initialized: true,
        loading: false,
        error: err?.message || String(err),
      });
      this.notify(convId);
    }
  }

  /**
   * 处理 WS 推送的消息
   */
  onWsMessage(convId: string, rawPayload: any): void {
    const msg = normalizeRealtimeMessageContent(rawPayload);
    if (!msg || !msg.message_id) return;

    const state = this.states.get(convId);
    if (state) {
      // 去重
      if (state.messages.some((m) => m.message_id === msg.message_id)) return;
      state.messages.push(msg);
      if ((msg.seq || 0) > state.maxSeq) {
        state.maxSeq = msg.seq || 0;
      }
    }

    // 持久化到 IndexedDB（含内部去重）
    appendMessageToCache(convId, msg);
    this.notify(convId);
  }

  /**
   * 手动添加消息（发送成功、Bot 消息完成等场景）
   */
  addMessage(convId: string, msg: Message): void {
    if (!msg || !msg.message_id) return;

    const state = this.states.get(convId);
    if (state) {
      if (state.messages.some((m) => m.message_id === msg.message_id)) return;
      state.messages.push(msg);
      state.messages.sort((a, b) => (a.seq || 0) - (b.seq || 0));
      if ((msg.seq || 0) > state.maxSeq) {
        state.maxSeq = msg.seq || 0;
      }
    }

    appendMessageToCache(convId, msg);
    this.notify(convId);
  }

  /**
   * 断线重连后增量同步
   */
  async reSync(convId: string): Promise<void> {
    const state = this.states.get(convId);
    if (!state) {
      return this.init(convId);
    }

    state.loading = true;
    this.notify(convId);

    try {
      const result = await msgApi.sync({
        conversation_id: convId as any,
        from_seq: state.maxSeq > 0 ? state.maxSeq : undefined,
      });

      const existingIds = new Set(state.messages.map((m) => m.message_id));
      for (const msg of result.messages) {
        if (!existingIds.has(msg.message_id)) {
          state.messages.push(msg);
        }
      }

      state.messages.sort((a, b) => (a.seq || 0) - (b.seq || 0));

      if (result.max_seq > state.maxSeq) {
        state.maxSeq = result.max_seq;
      }

      state.loading = false;
      state.error = null;

      saveMessageCache(convId, state.messages, state.maxSeq);
      this.notify(convId);
    } catch (err: any) {
      state.loading = false;
      state.error = err?.message || String(err);
      this.notify(convId);
    }
  }

  /**
   * 更新消息内容（撤回、编辑等场景）
   */
  updateMessage(convId: string, messageId: number, updater: (msg: Message) => Message): void {
    const state = this.states.get(convId);
    if (!state) return;
    let changed = false;
    state.messages = state.messages.map((m) => {
      if (m.message_id !== messageId) return m;
      changed = true;
      return updater(m);
    });
    if (!changed) return;
    updateMessageInCache(convId, messageId, updater);
    this.notify(convId);
  }

  /**
   * 删除消息
   */
  removeMessage(convId: string, messageId: number): void {
    const state = this.states.get(convId);
    if (state) {
      state.messages = state.messages.filter((m) => m.message_id !== messageId);
    }
    removeMessageFromCache(convId, messageId);
    this.notify(convId);
  }

  /**
   * 清理会话状态（切换会话时调用）
   */
  cleanup(convId: string): void {
    this.states.delete(convId);
  }
}

export const messageSync = new MessageSyncEngine();
