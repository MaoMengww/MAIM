import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { useQueries, useQuery, type UseQueryResult } from '@tanstack/react-query';
import { runReviewApi, type Review, type ReviewScope } from '@/services/runReview';
import { useAuthStore } from '@/stores/auth';

type Scope = ReviewScope & { bot_id: string };
type ReviewContextValue = {
  subscribe: (scope: Scope) => () => void;
  queries: Map<string, UseQueryResult<Review[], Error>>;
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
export function useReviewPolicy(scope: Scope) {
  const { userId, revision } = useReviewIdentity();
  return useQuery({
    queryKey: reviewPolicyKey(userId, revision, scope),
    queryFn: ({ signal }) => runReviewApi.getPolicy(scope.bot_id, { conv_id: scope.conv_id, visibility: scope.visibility }, signal),
    enabled: !!userId,
    retry: false,
    staleTime: 10000,
  });
}

// Share one list observer and polling timer per expanded Bot + scope.
export function RunReviewProvider({ children }: { children: ReactNode }) {
  const { userId, revision } = useReviewIdentity();
  const [subscriptions, setSubscriptions] = useState<Record<string, { scope: Scope; count: number }>>({});
  const scopes = Object.values(subscriptions).map((entry) => entry.scope);
  const results = useQueries({
    queries: scopes.map((scope) => ({
      queryKey: reviewListKey(userId, revision, scope),
      queryFn: ({ signal }: { signal: AbortSignal }) => runReviewApi.list(scope.bot_id, { conv_id: scope.conv_id, visibility: scope.visibility }, signal),
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
  return <ReviewContext.Provider value={{ subscribe, queries }}>{children}</ReviewContext.Provider>;
}

export function useRunReviews(botId: string, convId: string, visibility: ReviewScope['visibility']) {
  const context = useContext(ReviewContext);
  if (!context) throw new Error('复盘工作区未初始化');
  const { subscribe } = context;
  useEffect(() => subscribe({ bot_id: botId, conv_id: convId, visibility }), [subscribe, botId, convId, visibility]);
  return context.queries.get(scopeId({ bot_id: botId, conv_id: convId, visibility }));
}
