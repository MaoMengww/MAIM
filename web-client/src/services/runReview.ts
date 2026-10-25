import client, { unwrap } from './client';
import type { APIResponse } from '@/types/model';
import { entityId } from '@/utils/json';

export type ReviewVisibility = 'conversation' | 'personal';
export type ReviewStatus = 'queued' | 'assessing' | 'generating' | 'no_candidate' | 'candidate_generated' | 'failed' | 'over_budget' | 'cancelled';
export type ReviewTrigger = 'automatic' | 'feedback' | 'historical';

export interface ReviewAssessment {
  valuable: boolean;
  reason: string;
  target_version_id: string;
  target_goal: string;
  applicability: string;
}

export type SubmitRunReviewPayload = {
  parent_review_id?: string;
} & ({ trigger_kind: 'feedback'; feedback: string } | { trigger_kind: 'historical'; feedback?: never });
export type SubmitRunReviewReq = SubmitRunReviewPayload & { submission_key: string };

export interface ReviewPolicy {
  enabled: boolean;
  can_manage: boolean;
  scope_key: string;
  revision: number;
}

export interface ReviewCandidate {
  id: string;
  skill_id: string;
  source_run_id: string;
  publisher_id: string;
  scope_key: string;
  intent: 'create' | 'modify';
  target_goal: string;
  applicability: string;
  rationale: string;
  instructions: string;
  baseline_instructions: string;
  resources: Record<string, string>;
  scripts: Record<string, string>;
  image_digest: string;
  entry_point: string;
  digest: string;
  baseline_version_id?: string;
  status: string;
  code_review_required: boolean;
  evidence_digest: string;
  expires_at: number;
}

export interface Review {
  id: string;
  run_id: string;
  bot_id: string;
  conv_id: string;
  scope_key: string;
  status: ReviewStatus;
  revision: number;
  trigger_kind: ReviewTrigger;
  submitted_by?: string;
  parent_review_id?: string;
  feedback?: string;
  assessment?: ReviewAssessment;
  reason: string;
  model_calls: number;
  model_version: string;
  created_at: number;
  updated_at: number;
  expires_at: number;
  candidate_id?: string;
  candidate?: ReviewCandidate;
}

export interface ReviewScope {
  conv_id: string;
  visibility: ReviewVisibility;
}

export interface UpdateReviewPolicyReq extends ReviewScope {
  enabled: boolean;
  submission_key: string;
}

const REVIEW_STATUSES: readonly string[] = ['queued', 'assessing', 'generating', 'no_candidate', 'candidate_generated', 'failed', 'over_budget', 'cancelled'];

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('复盘数据格式无效');
  return value as Record<string, unknown>;
}
function text(value: unknown): string {
  if (typeof value !== 'string') throw new Error('复盘文本字段无效');
  return value;
}
function number(value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) throw new Error('复盘数值字段无效');
  return value;
}
function boolean(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new Error('复盘状态字段无效');
  return value;
}
function files(value: unknown): Record<string, string> {
  const entries = Object.entries(object(value));
  return Object.fromEntries(entries.map(([name, content]) => [name, text(content)]));
}
function normalizePolicy(value: unknown): ReviewPolicy {
  const data = object(value);
  return { enabled: boolean(data.enabled), can_manage: boolean(data.can_manage), scope_key: text(data.scope_key), revision: number(data.revision) };
}
function normalizeCandidate(value: unknown): ReviewCandidate {
  const data = object(value);
  if (data.intent !== 'create' && data.intent !== 'modify' || !['pending_validation', 'evaluating', 'evaluated', 'validation_failed', 'awaiting_evidence', 'approved', 'rejected', 'published', 'expired'].includes(text(data.status))) throw new Error('复盘候选状态无效');
  return {
    id: entityId(data.id, 'review.candidate_id'), skill_id: entityId(data.skill_id, 'review.skill_id'),
    source_run_id: entityId(data.source_run_id, 'review.source_run_id'), publisher_id: entityId(data.publisher_id, 'review.publisher_id'),
    scope_key: text(data.scope_key), intent: data.intent, target_goal: text(data.target_goal), applicability: text(data.applicability),
    rationale: text(data.rationale), instructions: text(data.instructions), baseline_instructions: text(data.baseline_instructions),
    resources: files(data.resources), scripts: files(data.scripts), image_digest: text(data.image_digest), entry_point: text(data.entry_point),
    digest: text(data.digest), baseline_version_id: data.baseline_version_id == null ? undefined : entityId(data.baseline_version_id, 'review.baseline_version_id'),
    status: text(data.status), code_review_required: boolean(data.code_review_required), evidence_digest: text(data.evidence_digest), expires_at: number(data.expires_at),
  };
}
function normalizeAssessment(value: unknown): ReviewAssessment {
  const data = object(value);
  return {
    valuable: boolean(data.valuable), reason: text(data.reason), target_version_id: text(data.target_version_id),
    target_goal: text(data.target_goal), applicability: text(data.applicability),
  };
}

export function reviewMetadata(review: Review): Review {
  return { ...review, feedback: undefined, assessment: undefined, candidate: undefined };
}

function normalizeReview(value: unknown): Review {
  const data = object(value);
  if (!REVIEW_STATUSES.includes(text(data.status))) throw new Error('复盘进展状态无效');
  if (!['automatic', 'feedback', 'historical'].includes(text(data.trigger_kind))) throw new Error('复盘来源无效');
  if (!Number.isSafeInteger(data.revision) || number(data.revision) < 1) throw new Error('复盘修订号无效');
  return {
    id: entityId(data.id, 'review.id'), run_id: entityId(data.run_id, 'review.run_id'), bot_id: entityId(data.bot_id, 'review.bot_id'),
    conv_id: entityId(data.conv_id, 'review.conv_id'), scope_key: text(data.scope_key), status: data.status as ReviewStatus,
    revision: number(data.revision), trigger_kind: data.trigger_kind as ReviewTrigger,
    submitted_by: data.submitted_by == null ? undefined : entityId(data.submitted_by, 'review.submitted_by'),
    parent_review_id: data.parent_review_id == null ? undefined : entityId(data.parent_review_id, 'review.parent_review_id'),
    feedback: data.feedback == null ? undefined : text(data.feedback),
    assessment: data.assessment == null ? undefined : normalizeAssessment(data.assessment),
    reason: text(data.reason), model_calls: number(data.model_calls), model_version: text(data.model_version),
    created_at: number(data.created_at), updated_at: number(data.updated_at), expires_at: number(data.expires_at),
    candidate_id: data.candidate_id == null ? undefined : entityId(data.candidate_id, 'review.candidate_id'),
    candidate: data.candidate == null ? undefined : normalizeCandidate(data.candidate),
  };
}

export const runReviewApi = {
  getPolicy: (botId: string, scope: ReviewScope, signal?: AbortSignal): Promise<ReviewPolicy> =>
    client.get<APIResponse<unknown>>(`/bots/${botId}/review-policy`, { params: scope, signal }).then((r) => normalizePolicy(unwrap(r))),
  updatePolicy: (botId: string, data: UpdateReviewPolicyReq): Promise<ReviewPolicy> =>
    client.put<APIResponse<unknown>>(`/bots/${botId}/review-policy`, data).then((r) => normalizePolicy(unwrap(r))),
  list: (botId: string, scope: ReviewScope, signal?: AbortSignal): Promise<Review[]> =>
    client.get<APIResponse<unknown>>(`/bots/${botId}/run-reviews`, { params: scope, signal }).then((r) => {
      const data = unwrap(r);
      if (!Array.isArray(data)) throw new Error('复盘列表必须是数组');
      return data.map((value) => reviewMetadata(normalizeReview(value)));
    }),
  get: (botId: string, reviewId: string, signal?: AbortSignal): Promise<Review> =>
    client.get<APIResponse<unknown>>(`/bots/${botId}/run-reviews/${reviewId}`, { signal }).then((r) => normalizeReview(unwrap(r))),
  submit: (botId: string, runId: string, data: SubmitRunReviewReq): Promise<Review> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/runs/${runId}/reviews`, data).then((r) => normalizeReview(unwrap(r))),
  cancel: (botId: string, reviewId: string, data: { submission_key: string }): Promise<Review> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/run-reviews/${reviewId}/cancel`, data).then((r) => normalizeReview(unwrap(r))),
};
