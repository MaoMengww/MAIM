import client, { unwrap } from './client';
import type { CreateBotReq, UpdateBotReq, StartRunReq } from '@/types/api';
import type { APIResponse, Bot, BotInConv, BotRun } from '@/types/model';
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

  startRun: (botId: string, data: StartRunReq): Promise<BotRun> =>
    client.post<APIResponse<unknown>>(`/bots/${botId}/runs`, data).then((r) => normalizeRun(unwrap(r))),

  getRun: (runId: string): Promise<BotRun> =>
    client.get<APIResponse<unknown>>(`/runs/${runId}`).then((r) => normalizeRun(unwrap(r))),

  listRuns: (convId: string): Promise<BotRun[]> =>
    client.get<APIResponse<unknown>>(`/conversations/${convId}/runs`, { params: { limit: 100 } }).then((r) => {
      const data = unwrap(r);
      if (!Array.isArray(data)) throw new Error('执行列表必须是数组');
      return data.map(normalizeRun);
    }),


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

// Project only the public contract: private snapshots and trajectory never enter UI state.
export function normalizeRun(value: unknown): BotRun {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('执行必须是对象');
  const run = value as Record<string, unknown>;
  const status = run.status;
  const output = run.output_status;
  if (status !== 'accepted' && status !== 'running' && status !== 'succeeded' && status !== 'failed') throw new Error('执行状态无效');
  if (output !== 'none' && output !== 'generated' && output !== 'pending' && output !== 'committed' && output !== 'failed') throw new Error('结果提交状态无效');
  const text = (value: unknown, field: string): string => {
    if (typeof value !== 'string') throw new Error(`${field} 必须是文本`);
    return value;
  };
  if (!Array.isArray(run.steps)) throw new Error('执行阶段必须是数组');
  if (!Array.isArray(run.sources)) throw new Error('执行来源必须是数组');
  if ((output === 'committed' && !run.message_id) || (status === 'succeeded' && output !== 'committed')) throw new Error('执行提交结果缺少最终消息确认');
  return {
    id: entityId(run.id, 'run.id'), interaction_id: entityId(run.interaction_id, 'run.interaction_id'),
    bot_id: entityId(run.bot_id, 'run.bot_id'), conv_id: entityId(run.conv_id, 'run.conv_id'),
    binding_id: entityId(run.binding_id, 'run.binding_id'), requester_id: entityId(run.requester_id, 'run.requester_id'),
    input_key: text(run.input_key, 'run.input_key'), source_message_id: optionalEntityId(run.source_message_id, 'run.source_message_id'),
    input_version: sequence(run.input_version, 'run.input_version', true), version: sequence(run.version, 'run.version', true),
    status, stage: text(run.stage, 'run.stage'), result_text: text(run.result_text, 'run.result_text'), output_status: output,
    message_id: optionalEntityId(run.message_id, 'run.message_id'), error: run.error == null ? undefined : text(run.error, 'run.error'),
    sources: run.sources.map((value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('执行来源格式无效');
      const source = value as Record<string, unknown>;
      if (source.type === 'rag') {
        return { type: 'rag' as const, kb_id: entityId(source.kb_id, 'source.kb_id'), doc_id: entityId(source.doc_id, 'source.doc_id'),
          chunk_id: entityId(source.chunk_id, 'source.chunk_id'), kb_name: text(source.kb_name, 'source.kb_name'), title: text(source.title, 'source.title') };
      }
      if (source.type === 'tool') return { type: 'tool' as const, tool_name: text(source.tool_name, 'source.tool_name') };
      const kind = source.type;
      const sender = source.sender_type;
      if (kind !== 'request_source' && kind !== 'reference' && kind !== 'history') throw new Error('执行来源类型无效');
      if (sender !== 'user' && sender !== 'bot' && sender !== 'system') throw new Error('执行来源发送者类型无效');
      return { type: kind, message_id: entityId(source.message_id, 'source.message_id'), sender_type: sender,
        sender_id: sender === 'system' ? optionalEntityId(source.sender_id === '' ? undefined : source.sender_id, 'source.sender_id') : entityId(source.sender_id, 'source.sender_id') };
    }), steps: run.steps.map((value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('执行阶段格式无效');
      const step = value as Record<string, unknown>;
      return { version: sequence(step.version, 'step.version', true), stage: text(step.stage, 'step.stage'), created_at: sequence(step.created_at, 'step.created_at') };
    }),
    created_at: sequence(run.created_at, 'run.created_at'), updated_at: sequence(run.updated_at, 'run.updated_at'),
  };
}

export function mergeRuns(previous: BotRun[] | undefined, updates: BotRun[]): BotRun[] {
  const runs = new Map((previous ?? []).map((run) => [run.id, run]));
  let changed = false;
  for (const update of updates) {
    const current = runs.get(update.id);
    if (!current || update.version > current.version) {
      runs.set(update.id, update);
      changed = true;
    }
  }
  if (previous && !changed) return previous;
  return [...runs.values()].sort((a, b) => a.created_at - b.created_at || a.id.localeCompare(b.id));
}
