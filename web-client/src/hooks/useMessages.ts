import { useState, useEffect, useCallback } from 'react';
import { messageSync } from '@/services/messageSync';
import type { Message } from '@/types/model';

export interface UseMessagesResult {
  messages: Message[];
  loading: boolean;
  error: string | null;
  reSync: () => void;
}

export function useMessages(convId: string | undefined): UseMessagesResult {
  const [version, setVersion] = useState(0);

  useEffect(() => {
    if (!convId) return;

    setVersion(0);

    // 初始化同步引擎（IndexedDB + 增量拉取）
    messageSync.init(convId);

    // 订阅变更通知
    const unsub = messageSync.subscribe((id: string) => {
      if (id === convId) {
        setVersion((v) => v + 1);
      }
    });

    return () => {
      unsub();
      // 不 cleanup state，以便返回时恢复
    };
  }, [convId]);

  const state = convId ? messageSync.getState(convId) : undefined;
  const messages = state?.messages ?? [];
  // 有缓存消息时不显示 loading，只有真正空状态等待时才显示
  const loading = !state ? true : (state.messages.length === 0 && state.loading);
  const error = state?.error ?? null;

  const reSync = useCallback(() => {
    if (convId) messageSync.reSync(convId);
  }, [convId]);

  return { messages, loading, error, reSync };
}
