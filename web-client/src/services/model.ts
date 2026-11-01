import client, { unwrap } from './client';
import type { CreateModelReq, UpdateModelReq } from '@/types/api';
import type { APIResponse, ModelResp, ModelCapability, BillingStatsResp, BillingRecordItem } from '@/types/model';

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
    client.delete<APIResponse<null>>(`/models/${id}`).then(unwrap),

  billingStats: (botId?: string) =>
    client.get<APIResponse<BillingStatsResp>>('/models/billing/stats', {
      params: botId ? { bot_id: botId } : undefined,
    }).then(unwrap),

  billingRecords: (page = 1, pageSize = 20) =>
    client.get<APIResponse<{ items: BillingRecordItem[]; total: number }>>('/models/billing/records', {
      params: { page, page_size: pageSize },
    }).then(unwrap),
};
