import client, { unwrap } from './client';
import type { CreateModelReq } from '@/types/api';
import type { APIResponse, ModelResp, BillingStatsResp, BillingRecordItem, PageData } from '@/types/model';

export const modelApi = {
  list: (capability?: string) =>
    client.get<APIResponse<{ items: ModelResp[]; total: number }>>('/models', {
      params: capability ? { capability } : undefined,
    }).then((r) => ({ list: r.data.data.items ?? [], total: r.data.data.total ?? 0 })),

  create: (data: CreateModelReq) =>
    client.post<APIResponse<ModelResp>>('/models', data).then(unwrap),

  update: (id: number, data: Record<string, unknown>) =>
    client.put<APIResponse<ModelResp>>(`/models/${id}`, data).then(unwrap),

  delete: (id: number) =>
    client.delete<APIResponse<null>>(`/models/${id}`).then(unwrap),

  billingStats: (botId?: number) =>
    client.get<APIResponse<BillingStatsResp>>('/models/billing/stats', {
      params: botId ? { bot_id: botId } : undefined,
    }).then(unwrap),

  billingRecords: (page = 1, pageSize = 20) =>
    client.get<APIResponse<{ items: BillingRecordItem[]; total: number }>>('/models/billing/records', {
      params: { page, page_size: pageSize },
    }).then(unwrap),
};
