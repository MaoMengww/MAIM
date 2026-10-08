import client, { unwrap } from './client';
import type { CreateBotReq, UpdateBotReq } from '@/types/api';
import type { APIResponse, Bot, BotInConv } from '@/types/model';
import { entityId, optionalEntityId, quantity, sequence } from '@/utils/json';

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

  listConvBots: (convId: string): Promise<BotInConv[]> =>
    client.get<APIResponse<{ bots: BotInConv[] }>>(`/convs/${convId}/bots`).then((r) => (r.data.data.bots ?? []).map((bot) => {
      if (bot.owner_type !== 'user' && bot.owner_type !== 'platform') throw new Error('Bot 所有者类型无效');
      return { ...bot, bot_id: entityId(bot.bot_id, 'bot.bot_id'), binding_id: entityId(bot.binding_id, 'bot.binding_id'),
        owner_id: optionalEntityId(bot.owner_id, 'bot.owner_id'), added_by: entityId(bot.added_by, 'bot.added_by'),
        added_at: quantity(bot.added_at, 'bot.added_at') };
    })),

  grantHistory: (convId: string, botId: string, bindingId: string, startSeq: number, endSeq: number) =>
    client.post<APIResponse<{ grant_id: string }>>(`/convs/${convId}/bots/${botId}/history-grants`, {
      binding_id: entityId(bindingId, 'grant.binding_id'), start_seq: sequence(startSeq, 'grant.start_seq', true),
      end_seq: sequence(endSeq, 'grant.end_seq', true),
    }).then((r) => ({ grant_id: entityId(r.data.data.grant_id, 'grant.grant_id') })),

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
