import client, { unwrap } from './client';
import type { SkillActionReq, SkillEnableReq, SkillImportReq } from '@/types/api';
import type { APIResponse, AvailableSkill, SkillCandidate, SkillEvaluation, SkillPackage, SkillVersion } from '@/types/model';
import { entityId } from '@/utils/json';

export function normalizeVersion(value: unknown): SkillVersion {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('技能版本格式无效');
  const version = value as Record<string, unknown>;
  if (typeof version.name !== 'string' || typeof version.description !== 'string' || typeof version.digest !== 'string'
    || typeof version.status !== 'string' || typeof version.enabled !== 'boolean'
    || typeof version.version !== 'number' || typeof version.created_at !== 'number') throw new Error('技能版本字段无效');
  return {
    version_id: entityId(version.version_id, 'skill.version_id'), version: version.version,
    name: version.name, description: version.description, digest: version.digest,
    status: version.status, enabled: version.enabled, created_at: version.created_at,
  };
}

const EVALUATION_STATUSES = ['passed', 'failed', 'insufficient', 'error'] as const;

export function normalizeEvaluation(value: unknown): SkillEvaluation {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('技能验证格式无效');
  const result = value as Record<string, unknown>;
  if (!EVALUATION_STATUSES.includes(result.status as (typeof EVALUATION_STATUSES)[number])
    || typeof result.passed !== 'boolean' || typeof result.reason !== 'string' || typeof result.digest !== 'string') {
    throw new Error('技能验证字段无效');
  }
  return {
    status: result.status as SkillEvaluation['status'], passed: result.passed, reason: result.reason, digest: result.digest,
    task_digest: String(result.task_digest ?? ''), expected_digest: String(result.expected_digest ?? ''),
    output_digest: String(result.output_digest ?? ''), model_version: String(result.model_version ?? ''),
    environment_digest: typeof result.environment_digest === 'string' ? result.environment_digest : undefined,
    created_at: Number(result.created_at ?? 0),
  };
}

function normalizeCandidate(value: unknown): SkillCandidate {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('技能候选格式无效');
  const candidate = value as Record<string, unknown>;
  if (typeof candidate.name !== 'string' || typeof candidate.description !== 'string' || typeof candidate.digest !== 'string'
    || typeof candidate.license !== 'string' || typeof candidate.status !== 'string') throw new Error('技能候选字段无效');
  const evaluation = candidate.evaluation == null ? undefined : normalizeEvaluation(candidate.evaluation);
  return {
    id: entityId(candidate.id, 'skill.candidate_id'), skill_id: entityId(candidate.skill_id, 'skill.skill_id'),
    name: candidate.name, description: candidate.description, digest: candidate.digest, license: candidate.license,
    status: candidate.status, failure: typeof candidate.failure === 'string' ? candidate.failure : undefined,
    baseline_version_id: candidate.baseline_version_id == null ? undefined : entityId(candidate.baseline_version_id, 'skill.baseline_version_id'),
    published_version_id: candidate.published_version_id == null ? undefined : entityId(candidate.published_version_id, 'skill.published_version_id'),
    approved_digest: typeof candidate.approved_digest === 'string' ? candidate.approved_digest : undefined,
    evaluation, created_at: Number(candidate.created_at ?? 0),
  };
}

function normalizePackage(value: unknown): SkillPackage {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('技能包格式无效');
  const pkg = value as Record<string, unknown>;
  if (typeof pkg.name !== 'string' || typeof pkg.license !== 'string' || !Array.isArray(pkg.versions) || !Array.isArray(pkg.candidates)) {
    throw new Error('技能包字段无效');
  }
  return {
    skill_id: entityId(pkg.skill_id, 'skill.skill_id'), name: pkg.name, license: pkg.license,
    publisher_id: entityId(pkg.publisher_id, 'skill.publisher_id'),
    enabled_version: pkg.enabled_version == null ? undefined : normalizeVersion(pkg.enabled_version),
    versions: pkg.versions.map(normalizeVersion), candidates: pkg.candidates.map(normalizeCandidate),
  };
}

export const skillApi = {
  list: (botId: string): Promise<SkillPackage[]> =>
    client.get<APIResponse<unknown>>(`/bots/${botId}/skills`).then((r) => {
      const data = unwrap(r);
      if (!Array.isArray(data)) throw new Error('技能列表必须是数组');
      return data.map(normalizePackage);
    }),

  catalog: (botId: string, signal?: AbortSignal): Promise<AvailableSkill[]> =>
    client.get<APIResponse<unknown>>(`/bots/${botId}/skill-catalog`, { signal }).then((r) => {
      const data = unwrap(r);
      if (!Array.isArray(data)) throw new Error('技能目录必须是数组');
      return data.map((value: unknown) => {
        if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('技能目录项格式无效');
        const item = value as Record<string, unknown>;
        if (typeof item.name !== 'string' || typeof item.description !== 'string' || typeof item.version !== 'number') {
          throw new Error('技能目录项字段无效');
        }
        return { version_id: entityId(item.version_id, 'skill.version_id'), name: item.name, description: item.description, version: item.version };
      });
    }),

  importPackage: (botId: string, data: SkillImportReq): Promise<SkillCandidate> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/skills`, data).then((r) => normalizeCandidate(unwrap(r))),

  getCandidate: (botId: string, candidateId: string): Promise<SkillCandidate> =>
    client.get<APIResponse<unknown>>(`/bots/${botId}/skills/${candidateId}`).then((r) => normalizeCandidate(unwrap(r))),

  evaluate: (botId: string, candidateId: string, data: SkillActionReq): Promise<SkillEvaluation> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/skills/${candidateId}/evaluate`, data)
      .then((r) => normalizeEvaluation(unwrap(r))),

  approve: (botId: string, candidateId: string, data: SkillActionReq): Promise<SkillCandidate> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/skills/${candidateId}/approve`, data).then((r) => normalizeCandidate(unwrap(r))),

  publish: (botId: string, candidateId: string, data: SkillActionReq): Promise<SkillVersion> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/skills/${candidateId}/publish`, data).then((r) => normalizeVersion(unwrap(r))),

  enable: (botId: string, versionId: string, data: SkillEnableReq): Promise<SkillVersion> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/skill-versions/${versionId}/enable`, data).then((r) => normalizeVersion(unwrap(r))),
};
