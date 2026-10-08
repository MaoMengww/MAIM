import { useEffect, useRef, useCallback } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { botApi, mergeRuns, normalizeRun } from '@/services/bot';
import { wsOn } from '@/services/ws';
import { messageSync } from '@/services/messageSync';
import { useAuthStore } from '@/stores/auth';
import { useWSStore } from '@/stores/ws';
import type { BotRun } from '@/types/model';

export function useRuns(convId: string) {
  const account = useAuthStore((state) => state.user?.id ?? '');
  const revision = useAuthStore((state) => state.revision);
  const reconnectVersion = useWSStore((state) => state.reconnectVersion);
  const connection = useWSStore((state) => state.status);
  const queryClient = useQueryClient();
  const queryKey = ['bot-runs', account, revision, convId] as const;
  const committed = useRef(new Set<string>());
  const query = useQuery({
    queryKey,
    queryFn: () => botApi.listRuns(convId),
    enabled: !!account && !!convId,
    // Queries repair missed updates; the WS connection is not a Run lifecycle.
    refetchInterval: 5000,
    structuralSharing: (previous, updates) => {
      // A successful list is authoritative for visibility. Keep newer versions
      // of listed records, but do not retain entries excluded by current access.
      const next = updates as BotRun[];
      const ids = new Set(next.map((run) => run.id));
      const visible = (previous as BotRun[] | undefined)?.filter((run) => ids.has(run.id));
      return mergeRuns(visible, next);
    },
  });

  const applyRun = useCallback((run: BotRun) => {
    if (run.conv_id !== convId || revision !== useAuthStore.getState().revision || account !== useAuthStore.getState().user?.id) return;
    queryClient.setQueryData<BotRun[]>(['bot-runs', account, revision, convId], (previous) => mergeRuns(previous, [run]));
  }, [queryClient, account, revision, convId]);

  useEffect(() => {
    return wsOn('bot.run.updated', (payload: unknown) => {
      if (!payload || typeof payload !== 'object') return;
      const event = payload as Record<string, unknown>;
      if (event.conv_id !== convId) return;
      try {
        const run = normalizeRun(event.run);
        if (run.bot_id !== event.bot_id) return;
        applyRun(run);
      } catch {
        void queryClient.invalidateQueries({ queryKey: ['bot-runs', account, revision, convId] });
      }
    });
  }, [queryClient, account, revision, convId, applyRun]);

  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: ['bot-runs', account, revision, convId] });
  }, [queryClient, account, revision, convId, reconnectVersion]);

  useEffect(() => {
    committed.current.clear();
    return () => { queryClient.removeQueries({ queryKey: ['bot-runs', account, revision, convId] }); };
  }, [queryClient, account, revision, convId]);
  useEffect(() => {
    let changed = false;
    for (const run of query.data ?? []) {
      if (run.output_status !== 'committed' || !run.message_id || committed.current.has(run.message_id)) continue;
      committed.current.add(run.message_id);
      changed = true;
    }
    // Never synthesize a message from generated text or from a Run event.
    if (changed) void messageSync.reSync();
  }, [query.data]);

  const refreshRun = async (runId: string) => {
    const run = await botApi.getRun(runId);
    applyRun(run);
  };
  return { ...query, runs: query.data ?? [], connection, applyRun, refreshRun };
}
