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
function normalizePolicy(value: unknown): ReviewPolicy {
  const data = object(value);
  return { enabled: boolean(data.enabled), can_manage: boolean(data.can_manage), scope_key: text(data.scope_key), revision: number(data.revision) };
}
function normalizeAssessment(value: unknown): ReviewAssessment {
  const data = object(value);
  return {
    valuable: boolean(data.valuable), reason: text(data.reason), target_version_id: text(data.target_version_id),
    target_goal: text(data.target_goal), applicability: text(data.applicability),
  };
}

export function reviewMetadata(review: Review): Review {
  if (review.feedback === undefined && review.assessment === undefined && !review.reason) return review;
  return { ...review, feedback: undefined, assessment: undefined, reason: '' };
}

function normalizeReview(value: unknown): Review {
  const data = object(value);
  if (!REVIEW_STATUSES.includes(text(data.status))) throw new Error('复盘进展状态无效');
  if (!['automatic', 'feedback', 'historical'].includes(text(data.trigger_kind))) throw new Error('复盘来源无效');
  if (!Number.isSafeInteger(data.revision) || number(data.revision) < 1) throw new Error('复盘修订号无效');
  const review: Review = {
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
  };
  return review.expires_at > 0 && review.expires_at * 1000 <= Date.now() ? reviewMetadata(review) : review;
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
