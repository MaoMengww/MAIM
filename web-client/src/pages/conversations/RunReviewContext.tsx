import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { useQueries, useQuery, useQueryClient, type QueryClient, type UseQueryResult } from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { reviewMetadata, runReviewApi, type Review, type ReviewScope, type SubmitRunReviewPayload } from '@/services/runReview';
import { useAuthStore } from '@/stores/auth';

type Scope = ReviewScope & { bot_id: string };
type ReviewSubmissionIntent = { current?: { signature: string; request: SubmitRunReviewPayload } };
type ReviewContextValue = {
  subscribe: (scope: Scope) => () => void;
  queries: Map<string, UseQueryResult<Review[], Error>>;
  submissionIntents: Map<string, ReviewSubmissionIntent>;
};
const ReviewContext = createContext<ReviewContextValue | null>(null);
const scopeId = (scope: Scope) => JSON.stringify([scope.bot_id, scope.conv_id, scope.visibility]);

export function useReviewIdentity() {
  const userId = useAuthStore((state) => state.user?.id);
  const revision = useAuthStore((state) => state.revision);
  return { userId, revision };
}

export function reviewPolicyKey(userId: string | undefined, revision: number, scope: Scope) {
  return ['review-policy', userId, revision, scope.bot_id, scope.conv_id, scope.visibility] as const;
}
export function reviewListKey(userId: string | undefined, revision: number, scope: Scope) {
  return ['run-reviews', userId, revision, scope.bot_id, scope.conv_id, scope.visibility] as const;
}
export function reviewDetailKey(userId: string | undefined, revision: number, botId: string, reviewId: string) {
  return ['run-review', userId, revision, botId, reviewId] as const;
}
export function isReviewPermissionError(error: unknown) {
  return isAxiosError(error) && (error.response?.status === 401 || error.response?.status === 403);
}

export async function clearReviewMaterials(queryClient: QueryClient, userId: string | undefined, revision: number, botId: string, readingReviewId?: string) {
  const detailKey = ['run-review', userId, revision, botId];
  const candidateKey = ['skill-candidate', userId, revision, botId];
  await Promise.all([
    queryClient.cancelQueries({ queryKey: detailKey, predicate: (query) => query.queryKey[4] !== readingReviewId }),
    queryClient.cancelQueries({ queryKey: candidateKey }),
  ]);
  queryClient.setQueriesData<Review>({ queryKey: detailKey }, (data) => data ? reviewMetadata(data) : data);
  queryClient.setQueriesData<Review[]>({ queryKey: ['run-reviews', userId, revision, botId] }, (data) => data?.map(reviewMetadata));
  queryClient.removeQueries({ queryKey: candidateKey });
}

export function useReviewPolicy(scope: Scope) {
  const { userId, revision } = useReviewIdentity();
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: reviewPolicyKey(userId, revision, scope),
    queryFn: async ({ signal }) => {
      try { return await runReviewApi.getPolicy(scope.bot_id, { conv_id: scope.conv_id, visibility: scope.visibility }, signal); }
      catch (error) {
        if (isReviewPermissionError(error)) await clearReviewMaterials(queryClient, userId, revision, scope.bot_id);
        throw error;
      }
    },
    enabled: !!userId,
    retry: false,
    staleTime: 10000,
  });
}

// Share one list observer and polling timer per expanded Bot + scope.
export function RunReviewProvider({ children }: { children: ReactNode }) {
  const { userId, revision } = useReviewIdentity();
  const queryClient = useQueryClient();
  const [subscriptions, setSubscriptions] = useState<Record<string, { scope: Scope; count: number }>>({});
  const submissionIntents = useMemo(() => new Map<string, ReviewSubmissionIntent>(), [userId, revision]);
  const scopes = Object.values(subscriptions).map((entry) => entry.scope);
  const results = useQueries({
    queries: scopes.map((scope) => ({
      queryKey: reviewListKey(userId, revision, scope),
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        try { return await runReviewApi.list(scope.bot_id, { conv_id: scope.conv_id, visibility: scope.visibility }, signal); }
        catch (error) {
          if (isReviewPermissionError(error)) await clearReviewMaterials(queryClient, userId, revision, scope.bot_id);
          throw error;
        }
      },
      enabled: !!userId,
      retry: false,
      staleTime: 4000,
      refetchInterval: (query: { state: { status: string } }) => query.state.status === 'error' ? false as const : 5000,
    })),
  });
  const subscribe = useMemo(() => (scope: Scope) => {
    const key = scopeId(scope);
    setSubscriptions((current) => ({ ...current, [key]: { scope, count: (current[key]?.count ?? 0) + 1 } }));
    return () => setSubscriptions((current) => {
      const entry = current[key];
      if (!entry) return current;
      const next = { ...current };
      if (entry.count === 1) delete next[key];
      else next[key] = { ...entry, count: entry.count - 1 };
      return next;
    });
  }, []);
  const queries = new Map(scopes.map((scope, index) => [scopeId(scope), results[index]]));
  return <ReviewContext.Provider value={{ subscribe, queries, submissionIntents }}>{children}</ReviewContext.Provider>;
}

export function useRunReviews(botId: string, convId: string, visibility: ReviewScope['visibility']) {
  const context = useContext(ReviewContext);
  if (!context) throw new Error('复盘工作区未初始化');
  const { subscribe } = context;
  useEffect(() => subscribe({ bot_id: botId, conv_id: convId, visibility }), [subscribe, botId, convId, visibility]);
  return context.queries.get(scopeId({ bot_id: botId, conv_id: convId, visibility }));
}

// Keep an uncertain submission stable across Run panel unmounts in this workspace.
// The identity-scoped map is memory-only and is discarded on account/session change.
export function useReviewSubmissionIntent(runId: string) {
  const context = useContext(ReviewContext);
  if (!context) throw new Error('复盘工作区未初始化');
  let intent = context.submissionIntents.get(runId);
  if (!intent) {
    intent = {};
    context.submissionIntents.set(runId, intent);
  }
  return intent;
}
