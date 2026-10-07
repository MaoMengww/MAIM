import { useState, useEffect, useCallback, useRef } from 'react';
import { messageSync } from '@/services/messageSync';
import { msgApi } from '@/services/message';
import type { Message } from '@/types/model';
import { useAuthStore } from '@/stores/auth';

export interface UseMessagesResult {
  messages: Message[];
  loading: boolean;
  error: string | null;
  reSync: () => void;
  loadOlder: () => void;
  hasOlder: boolean;
  loadingOlder: boolean;
}

export function useMessages(convId: string | undefined): UseMessagesResult {
  const revision = useAuthStore((state) => state.revision);
  const generation = useRef(0);
  const [, setVersion] = useState(0);
  const [cursor, setCursor] = useState(0);
  const [hasOlder, setHasOlder] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  useEffect(() => {
    if (!convId) return;
    return messageSync.subscribe((id) => {
      if (id === convId || id === '*') setVersion((v) => v + 1);
    });
  }, [convId]);
  useEffect(() => {
    let active = true;
    const currentGeneration = ++generation.current;
    setCursor(0);
    setHasOlder(false);
    setHistoryError(null);
    if (!convId) return;
    setLoadingOlder(true);
    void msgApi.history(convId).then(async (page) => {
      if (!active || currentGeneration !== generation.current || revision !== useAuthStore.getState().revision) return;
      for (const message of page.messages) {
        if (!active || currentGeneration !== generation.current || revision !== useAuthStore.getState().revision) return;
        await messageSync.addMessage(convId, message);
      }
      if (!active || currentGeneration !== generation.current || revision !== useAuthStore.getState().revision) return;
      setCursor(page.nextCursor);
      setHasOlder(page.hasMore);
    }).catch((error) => { if (active) setHistoryError(error instanceof Error ? error.message : '历史读取失败'); })
      .finally(() => { if (active) setLoadingOlder(false); });
    return () => { active = false; };
  }, [convId, revision]);
  const loadOlder = useCallback(() => {
    if (!convId || loadingOlder || !hasOlder) return;
    setLoadingOlder(true);
    setHistoryError(null);
    const currentGeneration = generation.current;
    void msgApi.history(convId, cursor).then(async (page) => {
      for (const message of page.messages) {
        if (currentGeneration !== generation.current || revision !== useAuthStore.getState().revision) return;
        await messageSync.addMessage(convId, message);
      }
      if (currentGeneration !== generation.current || revision !== useAuthStore.getState().revision) return;
      setCursor(page.nextCursor);
      setHasOlder(page.hasMore);
    }).catch((error) => { if (currentGeneration === generation.current) setHistoryError(error instanceof Error ? error.message : '历史读取失败'); })
      .finally(() => { if (currentGeneration === generation.current) setLoadingOlder(false); });
  }, [convId, cursor, loadingOlder, hasOlder, revision]);
  const state = convId ? messageSync.getState(convId) : undefined;
  return { messages: state?.messages ?? [], loading: !state || state.messages.length === 0 && (state.loading || loadingOlder),
    error: state?.error ?? historyError, reSync: () => { void messageSync.reSync(); }, loadOlder, hasOlder, loadingOlder };
}
