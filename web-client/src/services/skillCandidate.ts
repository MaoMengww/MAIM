import client, { unwrap } from './client';
import { normalizeEvaluation, normalizeVersion } from './skill';
import type { APIResponse, SkillEvaluation, SkillVersion } from '@/types/model';
import { entityId } from '@/utils/json';

export interface BaselineSample {
  version_id: string;
  representative_task: string;
  expected_output: string;
  input_b64: string;
  expected_output_b64: string;
}

export interface GovernedCandidate {
  id: string;
  // Archive imports have no source Run; source-scoped candidates still require one.
  origin?: 'archive';
  archive_baseline_stale?: boolean;
  skill_id: string;
  name: string;
  description: string;
  status: string;
  reason: string;
  digest: string;
  baseline_version_id?: string;
  current_version_id?: string | null;
  baseline_stale?: boolean;
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
  current_content?: {
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
  evidence_id: string;
  evidence_revision: number;
  source_license_expires_at: number;
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
  baseline_samples: BaselineSample[];
}

export type CandidateAction = 'code-review' | 'evaluate' | 'request-evidence' | 'reject' | 'approve' | 'publish' | 'revise' | 'reintegrate';
export interface CandidateRevision {
  name: string;
  description: string;
  rationale: string;
  target_goal: string;
  applicability: string;
  instructions: string;
  resources: Record<string, string>;
  scripts: Record<string, string>;
  image_digest: string;
  entry_point: string;
  representative_task: string;
  expected_output: string;
  input_b64: string;
  expected_output_b64: string;
  baseline_samples?: BaselineSample[];
  evidence_authorized: boolean;
  source_license_expires_at: number;
  expires_at?: number;
}
export interface CandidateActionRequest extends Partial<CandidateRevision> {
  submission_key: string;
  digest: string;
  confirmed?: boolean;
  reason?: string;
  current_version_id?: string;
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
  return Object.fromEntries(Object.entries(object(value ?? {})).map(([name, content]) => [name, text(content)]));
}
function baselineSamples(value: unknown): BaselineSample[] {
  if (value == null) return [];
  if (!Array.isArray(value) || value.length > 7) throw new Error('基线回归样例必须是最多 7 项的数组');
  return value.map((item: unknown) => {
    const sample = object(item);
    return {
      version_id: text(sample.version_id), representative_task: text(sample.representative_task),
      expected_output: text(sample.expected_output), input_b64: text(sample.input_b64),
      expected_output_b64: text(sample.expected_output_b64),
    };
  });
}
export function normalizeGovernedCandidate(value: unknown): GovernedCandidate {
  const data = object(value);
  if (typeof data.expires_at !== 'number' || !Number.isFinite(data.expires_at) || data.expires_at <= 0) throw new Error('候选材料期限无效');
  const canViewMaterials = permission(data.can_view_materials);
  const materialsCurrent = canViewMaterials && data.status !== 'expired' && data.expires_at * 1000 > Date.now();
  const noPublishedBaseline = data.baseline_stale === false && data.baseline_version_id == null && data.current_content == null;
  if (data.origin !== 'archive' && materialsCurrent && data.current_version_id !== null && typeof data.current_version_id !== 'string' && !noPublishedBaseline) {
    throw new Error('当前基线身份缺失，请刷新并重新核验材料权限');
  }
  const baseline = !materialsCurrent || data.baseline_content == null ? undefined : object(data.baseline_content);
  const current = !materialsCurrent || data.current_content == null ? undefined : object(data.current_content);
  const candidate: GovernedCandidate = {
    id: entityId(data.id, 'candidate.id'), skill_id: entityId(data.skill_id, 'candidate.skill_id'),
    source_run_id: data.origin === 'archive' ? text(data.source_run_id, true) : entityId(data.source_run_id, 'candidate.source_run_id'), publisher_id: entityId(data.publisher_id, 'candidate.publisher_id'),
    name: text(data.name, true), description: text(data.description, true), status: text(data.status), digest: text(data.digest),
    origin: data.origin === 'archive' ? 'archive' : undefined,
    archive_baseline_stale: data.origin === 'archive' && materialsCurrent ? permission(data.archive_baseline_stale ?? false) : undefined,
    reason: text(data.reason, true),
    baseline_content: baseline == null ? undefined : {
      instructions: text(baseline.instructions), resources: files(baseline.resources), scripts: files(baseline.scripts),
      image_digest: text(baseline.image_digest), entry_point: text(baseline.entry_point),
    },
    current_content: current == null ? undefined : {
      instructions: text(current.instructions), resources: files(current.resources), scripts: files(current.scripts),
      image_digest: text(current.image_digest), entry_point: text(current.entry_point),
    },
    baseline_version_id: data.baseline_version_id == null ? undefined : entityId(data.baseline_version_id, 'candidate.baseline_version_id'),
    current_version_id: !materialsCurrent ? undefined : data.current_version_id == null ? null : entityId(data.current_version_id, 'candidate.current_version_id'),
    baseline_stale: data.origin === 'archive' ? undefined : materialsCurrent ? permission(data.baseline_stale) : undefined,
    published_version_id: data.published_version_id == null ? undefined : entityId(data.published_version_id, 'candidate.published_version_id'),
    scope_key: text(data.scope_key), instructions: text(data.instructions, true), baseline_instructions: text(data.baseline_instructions, true),
    resources: files(data.resources), scripts: files(data.scripts), image_digest: text(data.image_digest, true), entry_point: text(data.entry_point, true),
    target_goal: text(data.target_goal, true), applicability: text(data.applicability, true), rationale: text(data.rationale, true),
    evidence_digest: text(data.evidence_digest, true), evidence: text(data.evidence, true), expires_at: data.expires_at,
    evidence_id: text(data.evidence_id, true), evidence_revision: typeof data.evidence_revision === 'number' ? data.evidence_revision : 0,
    source_license_expires_at: typeof data.evidence_license_expires_at === 'number' ? data.evidence_license_expires_at : 0,
    code_review_required: permission(data.code_review_required), code_review_digest: text(data.code_review_digest, true),
    evaluation: data.evaluation == null ? undefined : normalizeEvaluation(data.evaluation),
    can_maintain: permission(data.can_maintain), can_view_materials: canViewMaterials,
    can_review_code: permission(data.can_review_code), can_publish: permission(data.can_publish),
    representative_task: text(data.representative_task, true), expected_output: text(data.expected_output, true),
    input_b64: text(data.input_b64, true), expected_output_b64: text(data.expected_output_b64, true),
    baseline_samples: baselineSamples(data.baseline_samples),
  };
  return candidateEvidenceExpired(candidate) || !candidate.can_view_materials ? candidateMetadata(candidate) : candidate;
}

export function candidateEvidenceExpired(candidate: GovernedCandidate, now = Date.now()): boolean {
  return candidate.status === 'expired' || candidate.expires_at * 1000 <= now;
}

// Retain only identifiers, lifecycle and authorization metadata, never old material.
export function candidateMetadata(candidate: GovernedCandidate): GovernedCandidate {
  const expired = candidateEvidenceExpired(candidate);
  const status = expired && !['published', 'rejected'].includes(candidate.status) ? 'expired' : candidate.status;
  const reason = expired ? '证据留存期限已到；须提交新的获准样例和具体预期，重新验证及批准。' : '';
  if (!candidate.name && !candidate.description && !candidate.instructions && !candidate.baseline_instructions && !candidate.baseline_content && !candidate.current_content
    && !Object.keys(candidate.resources).length && !Object.keys(candidate.scripts).length && !candidate.image_digest && !candidate.entry_point
    && !candidate.target_goal && !candidate.applicability && !candidate.rationale && !candidate.evidence && !candidate.evaluation
    && !candidate.representative_task && !candidate.expected_output && !candidate.input_b64 && !candidate.expected_output_b64
    && !candidate.baseline_samples.length && candidate.baseline_stale === undefined && candidate.current_version_id === undefined && candidate.archive_baseline_stale === undefined
    && !candidate.code_review_digest && !candidate.can_view_materials && !candidate.can_review_code && !candidate.can_publish
    && candidate.status === status && candidate.reason === reason) return candidate;
  return { ...candidate, name: '', description: '', instructions: '', baseline_instructions: '', baseline_content: undefined, current_content: undefined,
    baseline_stale: undefined, current_version_id: undefined, archive_baseline_stale: undefined,
    resources: {}, scripts: {}, image_digest: '', entry_point: '', target_goal: '', applicability: '', rationale: '', evidence: '',
    representative_task: '', expected_output: '', input_b64: '', expected_output_b64: '', evaluation: undefined, code_review_digest: '',
    baseline_samples: [],
    can_view_materials: false, can_review_code: false, can_publish: false, status, reason };
}

const base = (botId: string) => `/bots/${botId}/skill-candidates`;
export const skillCandidateApi = {
  importArchive: (botId: string, request: { archive: string; submission_key: string; representative_task: string; expected_output: string; input_b64: string; expected_output_b64: string; evidence_authorized: boolean; source_license_expires_at: number }): Promise<GovernedCandidate> =>
    client.post<APIResponse<unknown>>(`${base(botId)}/import`, request).then((response) => normalizeGovernedCandidate(unwrap(response))),
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
