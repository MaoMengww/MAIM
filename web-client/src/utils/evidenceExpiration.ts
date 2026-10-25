import type { QueryClient } from '@tanstack/react-query';
import { candidateEvidenceExpired, candidateMetadata, type GovernedCandidate } from '@/services/skillCandidate';
import { reviewMetadata, type Review } from '@/services/runReview';

// The guard belongs to the QueryClient, not an open panel: closing a candidate
// or suspending a tab must not leave its expired material in the cache.
export function installEvidenceExpiration(queryClient: QueryClient): () => void {
  let timer: number | undefined;
  let sweeping = false;
  const sweep = () => {
    if (sweeping) return;
    sweeping = true;
    window.clearTimeout(timer);
    let nextExpiry = Infinity;
    const now = Date.now();
    const candidate = (value: GovernedCandidate, failed: boolean) => {
      if (failed || candidateEvidenceExpired(value, now)) return candidateMetadata(value);
      nextExpiry = Math.min(nextExpiry, value.expires_at * 1000);
      return value;
    };
    const review = (value: Review, failed: boolean) => {
      const expires = value.expires_at > 0 ? value.expires_at * 1000 : Infinity;
      if (failed || expires <= now) return reviewMetadata(value);
      nextExpiry = Math.min(nextExpiry, expires);
      return value;
    };
    try {
      for (const query of queryClient.getQueryCache().getAll()) {
        const kind = query.queryKey[0];
        const data = query.state.data;
        if (!data) continue;
        const failed = query.state.status === 'error';
        let clean: unknown = data;
        if (kind === 'skill-candidate') clean = candidate(data as GovernedCandidate, failed);
        else if (kind === 'skill-candidates') {
          const values = data as GovernedCandidate[];
          const updated = values.map((value) => candidate(value, failed));
          if (updated.some((value, index) => value !== values[index])) clean = updated;
        } else if (kind === 'run-review') clean = review(data as Review, failed);
        else if (kind === 'run-reviews') {
          const values = data as Review[];
          const updated = values.map((value) => review(value, failed));
          if (updated.some((value, index) => value !== values[index])) clean = updated;
        }
        if (clean !== data) query.setState({ data: clean });
      }
      // Mutation variables can contain uploaded samples as well as the response.
      for (const mutation of queryClient.getMutationCache().getAll()) {
        const expires = mutation.meta?.evidence_expires_at;
        if (typeof expires !== 'number') continue;
        if (expires * 1000 <= now) queryClient.getMutationCache().remove(mutation);
        else nextExpiry = Math.min(nextExpiry, expires * 1000);
      }
    } finally {
      sweeping = false;
    }
    if (Number.isFinite(nextExpiry)) timer = window.setTimeout(sweep, Math.min(Math.max(nextExpiry - now, 1), 2147483647));
  };
  const unsubscribeQuery = queryClient.getQueryCache().subscribe(sweep);
  const unsubscribeMutation = queryClient.getMutationCache().subscribe(sweep);
  window.addEventListener('focus', sweep);
  window.addEventListener('pageshow', sweep);
  document.addEventListener('visibilitychange', sweep);
  sweep();
  return () => {
    window.clearTimeout(timer);
    unsubscribeQuery();
    unsubscribeMutation();
    window.removeEventListener('focus', sweep);
    window.removeEventListener('pageshow', sweep);
    document.removeEventListener('visibilitychange', sweep);
  };
}
