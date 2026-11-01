import client, { unwrap } from './client';
import type {
  CreateModelReq, UpdateModelReq, CreateModelConnectionReq, UpdateModelConnectionReq,
  CreateQuotaPoolReq, UpdateQuotaPoolReq,
} from '@/types/api';
import type {
  APIResponse, ModelResp, ModelCapability, BillingStatsResp, BillingRecordItem,
  ModelConnectionResp, QuotaPoolResp,
} from '@/types/model';

export interface ModelManagementList<T> {
  list: T[];
  total: number;
}

// Selection controls must include every owned resource, not only the first server page.
async function listAllOwned<T>(path: string): Promise<ModelManagementList<T>> {
  const list: T[] = [];
  let total = 0;
  for (let page = 1; ; page += 1) {
    const response = await client.get<APIResponse<{ items: T[]; total: number }>>(path, {
      params: { page, page_size: 100 },
    });
    const items = response.data.data.items ?? [];
    total = response.data.data.total ?? 0;
    list.push(...items);
    if (list.length >= total || items.length === 0) break;
  }
  return { list, total };
}

export const modelApi = {
  list: (capability?: ModelCapability) =>
    client.get<APIResponse<{ items: ModelResp[]; total: number }>>('/models', {
      params: capability ? { capability } : undefined,
    }).then((r) => ({ list: r.data.data.items ?? [], total: r.data.data.total ?? 0 })),

  create: (data: CreateModelReq) =>
    client.post<APIResponse<ModelResp>>('/models', data).then(unwrap),

  update: (id: string, data: UpdateModelReq) =>
    client.put<APIResponse<{ id: string }>>(`/models/${id}`, data).then(unwrap),

  delete: (id: string) =>
    client.delete<APIResponse<{ success: boolean }>>(`/models/${id}`).then(unwrap),

  billingStats: (botId?: string) =>
    client.get<APIResponse<BillingStatsResp>>('/models/billing/stats', {
      params: botId ? { bot_id: botId } : undefined,
    }).then(unwrap),

  billingRecords: (page = 1, pageSize = 20) =>
    client.get<APIResponse<{ items: BillingRecordItem[]; total: number }>>('/models/billing/records', {
      params: { page, page_size: pageSize },
    }).then(unwrap),
};

export const modelConnectionApi = {
  list: () => listAllOwned<ModelConnectionResp>('/model-connections'),

  create: (data: CreateModelConnectionReq) =>
    client.post<APIResponse<ModelConnectionResp>>('/model-connections', data).then(unwrap),

  update: (id: string, data: UpdateModelConnectionReq) =>
    client.put<APIResponse<ModelConnectionResp>>(`/model-connections/${id}`, data).then(unwrap),

  delete: (id: string) =>
    client.delete<APIResponse<{ success: boolean }>>(`/model-connections/${id}`).then(unwrap),
};

export const quotaPoolApi = {
  list: () => listAllOwned<QuotaPoolResp>('/quota-pools'),

  create: (data: CreateQuotaPoolReq) =>
    client.post<APIResponse<QuotaPoolResp>>('/quota-pools', data).then(unwrap),

  update: (id: string, data: UpdateQuotaPoolReq) =>
    client.put<APIResponse<QuotaPoolResp>>(`/quota-pools/${id}`, data).then(unwrap),

  delete: (id: string) =>
    client.delete<APIResponse<{ success: boolean }>>(`/quota-pools/${id}`).then(unwrap),
};
