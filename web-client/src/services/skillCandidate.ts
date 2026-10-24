import client, { unwrap } from './client';
import { normalizeEvaluation, normalizeVersion } from './skill';
import type { APIResponse, SkillEvaluation, SkillVersion } from '@/types/model';
import { entityId } from '@/utils/json';

export interface GovernedCandidate {
  id: string;
  skill_id: string;
  name: string;
  description: string;
  status: string;
  reason: string;
  digest: string;
  baseline_version_id?: string;
  published_version_id?: string;
  source_run_id: string;
  publisher_id: string;
  scope_key: string;
  instructions: string;
  baseline_instructions: string;
  baseline_content?: {
    instructions: string;
    resources: Record<string, string>;
    scripts: Record<string, string>;
    image_digest: string;
    entry_point: string;
  };
  resources: Record<string, string>;
  scripts: Record<string, string>;
  image_digest: string;
  entry_point: string;
  target_goal: string;
  applicability: string;
  rationale: string;
  evidence_digest: string;
  evidence: string;
  expires_at: number;
  code_review_required: boolean;
  code_review_digest: string;
  evaluation?: SkillEvaluation;
  can_maintain: boolean;
  can_view_materials: boolean;
  can_review_code: boolean;
  can_publish: boolean;
  representative_task: string;
  expected_output: string;
  input_b64: string;
  expected_output_b64: string;
}

export type CandidateAction = 'code-review' | 'evaluate' | 'request-evidence' | 'reject' | 'approve' | 'publish' | 'revise';
export interface CandidateRevision {
  instructions: string;
  resources: Record<string, string>;
  scripts: Record<string, string>;
  image_digest: string;
  entry_point: string;
  representative_task: string;
  expected_output: string;
  input_b64: string;
  expected_output_b64: string;
  expires_at: number;
}
export interface CandidateActionRequest extends Partial<CandidateRevision> {
  submission_key: string;
  digest: string;
  confirmed?: boolean;
  reason?: string;
}

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('候选数据格式无效');
  return value as Record<string, unknown>;
}
function text(value: unknown, optional = false): string {
  if (optional && value == null) return '';
  if (typeof value !== 'string') throw new Error('候选文本字段无效');
  return value;
}
function permission(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new Error('候选权限字段缺失，不能授权操作');
  return value;
}
function files(value: unknown): Record<string, string> {
  return Object.fromEntries(Object.entries(object(value)).map(([name, content]) => [name, text(content)]));
}
export function normalizeGovernedCandidate(value: unknown): GovernedCandidate {
  const data = object(value);
  if (typeof data.expires_at !== 'number' || !Number.isFinite(data.expires_at) || data.expires_at <= 0) throw new Error('候选材料期限无效');
  const baseline = data.baseline_content == null ? undefined : object(data.baseline_content);
  return {
    id: entityId(data.id, 'candidate.id'), skill_id: entityId(data.skill_id, 'candidate.skill_id'),
    source_run_id: entityId(data.source_run_id, 'candidate.source_run_id'), publisher_id: entityId(data.publisher_id, 'candidate.publisher_id'),
    name: text(data.name, true), description: text(data.description, true), status: text(data.status), digest: text(data.digest),
    reason: text(data.reason, true),
    baseline_content: baseline == null ? undefined : {
      instructions: text(baseline.instructions), resources: files(baseline.resources), scripts: files(baseline.scripts),
      image_digest: text(baseline.image_digest), entry_point: text(baseline.entry_point),
    },
    baseline_version_id: data.baseline_version_id == null ? undefined : entityId(data.baseline_version_id, 'candidate.baseline_version_id'),
    published_version_id: data.published_version_id == null ? undefined : entityId(data.published_version_id, 'candidate.published_version_id'),
    scope_key: text(data.scope_key), instructions: text(data.instructions), baseline_instructions: text(data.baseline_instructions),
    resources: files(data.resources), scripts: files(data.scripts), image_digest: text(data.image_digest), entry_point: text(data.entry_point),
    target_goal: text(data.target_goal), applicability: text(data.applicability), rationale: text(data.rationale),
    evidence_digest: text(data.evidence_digest), evidence: text(data.evidence, true), expires_at: data.expires_at,
    code_review_required: permission(data.code_review_required), code_review_digest: text(data.code_review_digest, true),
    evaluation: data.evaluation == null ? undefined : normalizeEvaluation(data.evaluation),
    can_maintain: permission(data.can_maintain), can_view_materials: permission(data.can_view_materials),
    can_review_code: permission(data.can_review_code), can_publish: permission(data.can_publish),
    representative_task: text(data.representative_task, true), expected_output: text(data.expected_output, true),
    input_b64: text(data.input_b64, true), expected_output_b64: text(data.expected_output_b64, true),
  };
}

const base = (botId: string) => `/bots/${botId}/skill-candidates`;
export const skillCandidateApi = {
  list: (botId: string, signal?: AbortSignal): Promise<GovernedCandidate[]> =>
    client.get<APIResponse<unknown>>(base(botId), { signal }).then((response) => {
      const data = unwrap(response);
      if (!Array.isArray(data)) throw new Error('候选列表格式无效');
      return data.map(normalizeGovernedCandidate);
    }),
  get: (botId: string, candidateId: string, signal?: AbortSignal): Promise<GovernedCandidate> =>
    client.get<APIResponse<unknown>>(`${base(botId)}/${candidateId}`, { signal }).then((response) => normalizeGovernedCandidate(unwrap(response))),
  act: (botId: string, candidateId: string, action: CandidateAction, request: CandidateActionRequest): Promise<GovernedCandidate | SkillEvaluation | SkillVersion> =>
    client.post<APIResponse<unknown>>(`${base(botId)}/${candidateId}/${action}`, request, { timeout: action === 'evaluate' ? 180000 : 30000 })
      .then((response) => action === 'evaluate' ? normalizeEvaluation(unwrap(response))
        : action === 'publish' ? normalizeVersion(unwrap(response)) : normalizeGovernedCandidate(unwrap(response))),
};
