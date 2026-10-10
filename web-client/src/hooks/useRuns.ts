import { useEffect, useRef, useCallback } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { botApi, mergeRuns } from '@/services/bot';
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
  const removed = useRef(false);
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      try { return await botApi.listRuns(convId, signal); }
      catch (error) {
        // A failed current-authority read cannot leave the previous projection
        // visible indefinitely while query retries or the transport is offline.
        if (!signal.aborted) queryClient.setQueryData<BotRun[]>(queryKey, []);
        throw error;
      }
    },
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
    if (run.visibility !== 'conversation' || removed.current || run.conv_id !== convId || revision !== useAuthStore.getState().revision || account !== useAuthStore.getState().user?.id) return;
    // A mutation response is not a viewer-visibility lease. Rebuild through REST.
    void queryClient.invalidateQueries({ queryKey: ['bot-runs', account, revision, convId] });
  }, [queryClient, account, revision, convId]);

  useEffect(() => {
    return wsOn('bot.run.updated', (payload: unknown) => {
      if (!payload || typeof payload !== 'object') return;
      const event = payload as Record<string, unknown>;
      if (event.conv_id !== convId) return;
      // Shared events are notification only. Drop the affected projection before
      // rechecking this viewer's current authority; never apply the event body.
      const eventRun = event.run;
      const runId = eventRun && typeof eventRun === 'object' ? (eventRun as Record<string, unknown>).id : undefined;
      void queryClient.cancelQueries({ queryKey: ['bot-runs', account, revision, convId] }).then(() => {
        queryClient.setQueryData<BotRun[]>(['bot-runs', account, revision, convId], (previous) => previous?.filter((run) => run.id !== runId));
        return queryClient.invalidateQueries({ queryKey: ['bot-runs', account, revision, convId] });
      });
    });
  }, [queryClient, account, revision, convId, applyRun]);

  useEffect(() => {
    void queryClient.cancelQueries({ queryKey: ['bot-runs', account, revision, convId] }).then(() => {
      queryClient.setQueryData<BotRun[]>(['bot-runs', account, revision, convId], []);
      return queryClient.invalidateQueries({ queryKey: ['bot-runs', account, revision, convId] });
    });
  }, [queryClient, account, revision, convId, reconnectVersion]);

  useEffect(() => {
    removed.current = false;
    committed.current.clear();
    return () => { queryClient.removeQueries({ queryKey: ['bot-runs', account, revision, convId] }); };
  }, [queryClient, account, revision, convId]);
  useEffect(() => messageSync.subscribe((_id, snapshotChanged, removedConversations, invalidatedWork) => {
    if (removedConversations.includes(convId)) {
      removed.current = true;
      committed.current.clear();
      void queryClient.cancelQueries({ queryKey: ['bot-runs', account, revision, convId] });
      queryClient.setQueryData<BotRun[]>(['bot-runs', account, revision, convId], []);
      return;
    }
    if (invalidatedWork.includes(convId)) {
      // Remove the stale derived view before an authoritative response arrives.
      // Cancellation prevents an older in-flight response restoring it.
      void queryClient.cancelQueries({ queryKey: ['bot-runs', account, revision, convId] }).then(() => {
        queryClient.setQueryData<BotRun[]>(['bot-runs', account, revision, convId], []);
        return queryClient.invalidateQueries({ queryKey: ['bot-runs', account, revision, convId] });
      });
      return;
    }
    if (snapshotChanged && messageSync.getConversations().some((conversation) => conversation.id === convId)) {
      removed.current = false;
      void queryClient.invalidateQueries({ queryKey: ['bot-runs', account, revision, convId] });
    }
  }), [queryClient, account, revision, convId]);
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
