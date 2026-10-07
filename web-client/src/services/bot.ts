import client, { unwrap } from './client';
import type { CreateBotReq, UpdateBotReq } from '@/types/api';
import type { APIResponse, Bot } from '@/types/model';

export const botApi = {
  create: (data: CreateBotReq) =>
    client.post<APIResponse<Bot>>('/bots', data).then(unwrap),

  update: (id: string, data: UpdateBotReq) =>
    client.put<APIResponse<Bot>>(`/bots/${id}`, data).then(unwrap),

  delete: (id: string) =>
    client.delete<APIResponse<null>>(`/bots/${id}`).then(unwrap),

  get: (id: string) =>
    client.get<APIResponse<Bot>>(`/bots/${id}`).then(unwrap),

  list: (params?: { status?: string }) =>
    client.get<APIResponse<{ bots: Bot[]; pagination?: any }>>('/bots', { params })
      .then((r) => ({ list: r.data.data.bots ?? [], total: r.data.data.pagination?.total ?? 0 })),

  rotateSecret: (id: string) =>
    client.post<APIResponse<{ webhook_secret: string; app_secret: string }>>(`/bots/${id}/secret/rotate`).then(unwrap),

  issueToken: (id: string, ttlSeconds?: number) =>
    client.post<APIResponse<{ token: string; expires_at: number }>>(`/bots/${id}/token`, { ttl_seconds: ttlSeconds }).then(unwrap),

  // Streaming chat URL (SSE)
  streamUrl: (botId: string, message: string, convId?: string) => {
    const base = import.meta.env.VITE_API_BASE || 'http://localhost:8080';
    let url = `${base}/api/v1/bots/${botId}/chat/stream?message=${encodeURIComponent(message)}`;
    if (convId) url += `&conv_id=${convId}`;
    return url;
  },


  // Bot in conversation
  addToConv: (convId: string, botId: string) =>
    client.post<APIResponse<null>>(`/convs/${convId}/bots`, { bot_id: botId }).then(unwrap),

  removeFromConv: (convId: string, botId: string) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/bots/${botId}`).then(unwrap),

  listConvBots: (convId: string) =>
    client.get<APIResponse<{ bots: any[] }>>(`/convs/${convId}/bots`).then((r) => r.data.data.bots ?? []),

  // Upload bot avatar
  uploadAvatar: (botId: string, file: File) => {
    const fd = new FormData();
    fd.append('file', file);
    return client.post<APIResponse<any>>(`/bots/${botId}/avatar`, fd).then(unwrap);
  },

  // Create or get a 1-on-1 conversation with a bot
  createConversation: (botId: string) =>
    client.post<APIResponse<{ conversation_id: string; conversation: any }>>('/convs', {
      type: 'single',
      bot_id: botId,
    }).then((r) => {
      const data = r.data.data;
      return { id: data.conversation_id, conversation: data.conversation };
    }),
};
