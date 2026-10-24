import type { ReviewCandidate } from '@/services/runReview';
import { CandidateGovernance } from '@/components/skills/CandidateGovernance';

export function ReviewCandidateDetails({ botId, candidate }: { botId: string; candidate: ReviewCandidate }) {
  // Read the current authorized candidate rather than acting on the review's
  // historical projection or treating model-generated material as approval.
  return <CandidateGovernance botId={botId} candidateId={candidate.id} />;
}
