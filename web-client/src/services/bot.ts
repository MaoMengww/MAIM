import client, { unwrap } from './client';
import type { CreateBotReq, UpdateBotReq } from '@/types/api';
import type { APIResponse, Bot, BotMemory, Conversation } from '@/types/model';

export const botApi = {
  create: (data: CreateBotReq) =>
    client.post<APIResponse<Bot>>('/bots', data).then(unwrap),

  update: (id: number, data: UpdateBotReq) =>
    client.put<APIResponse<Bot>>(`/bots/${id}`, data).then(unwrap),

  delete: (id: number) =>
    client.delete<APIResponse<null>>(`/bots/${id}`).then(unwrap),

  get: (id: number) =>
    client.get<APIResponse<Bot>>(`/bots/${id}`).then(unwrap),

  list: (params?: { status?: string }) =>
    client.get<APIResponse<{ bots: Bot[]; pagination?: any }>>('/bots', { params })
      .then((r) => ({ list: r.data.data.bots ?? [], total: r.data.data.pagination?.total ?? 0 })),

  rotateSecret: (id: number) =>
    client.post<APIResponse<{ webhook_secret: string; app_secret: string }>>(`/bots/${id}/secret/rotate`).then(unwrap),

  issueToken: (id: number, ttlSeconds?: number) =>
    client.post<APIResponse<{ token: string; expires_at: number }>>(`/bots/${id}/token`, { ttl_seconds: ttlSeconds }).then(unwrap),

  // Streaming chat URL (SSE)
  streamUrl: (botId: number, message: string, convId?: number) => {
    const base = import.meta.env.VITE_API_BASE || 'http://localhost:8080';
    let url = `${base}/api/v1/bots/${botId}/chat/stream?message=${encodeURIComponent(message)}`;
    if (convId) url += `&conv_id=${convId}`;
    return url;
  },

  // Memory
  getMemory: (botId: string | number) =>
    client.get<APIResponse<{ items: BotMemory[] }>>(`/bots/${botId}/memory`).then((r) => r.data.data?.items ?? []),

  clearMemory: (botId: string | number) =>
    client.delete<APIResponse<null>>(`/bots/${botId}/memory`).then(unwrap),

  forgetMemory: (botId: string | number, memoryId: number) =>
    client.delete<APIResponse<null>>(`/bots/${botId}/memory/${memoryId}`).then(unwrap),

  // Bot in conversation
  addToConv: (convId: number, botId: string) =>
    client.post<APIResponse<null>>(`/convs/${convId}/bots`, { bot_id: botId }).then(unwrap),

  removeFromConv: (convId: number, botId: string) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/bots/${botId}`).then(unwrap),

  listConvBots: (convId: number) =>
    client.get<APIResponse<{ bots: any[] }>>(`/convs/${convId}/bots`).then((r) => r.data.data.bots ?? []),

  // Upload bot avatar
  uploadAvatar: (botId: string | number, file: File) => {
    const fd = new FormData();
    fd.append('file', file);
    return client.post<APIResponse<any>>(`/bots/${botId}/avatar`, fd).then(unwrap);
  },

  // Create or get a 1-on-1 conversation with a bot
  createConversation: (pseudoUserId: string | number) =>
    client.post<APIResponse<{ conversation_id: string; conversation: any }>>('/convs', {
      type: 'single',
      peer_user_id: String(pseudoUserId),
    }).then((r) => {
      const data = r.data.data;
      return { id: data.conversation_id, conversation: data.conversation };
    }),
};
