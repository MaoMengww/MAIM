import client, { unwrap } from './client';
import type { CreateBotReq, UpdateBotReq, StartRunReq, ControlRunReq } from '@/types/api';
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

  controlRun: (runId: string, data: ControlRunReq): Promise<BotRun> =>
    client.post<APIResponse<unknown>>(`/runs/${runId}/control`, data).then((r) => normalizeRun(unwrap(r))),

  listRuns: (convId: string, signal?: AbortSignal): Promise<BotRun[]> =>
    client.get<APIResponse<unknown>>(`/conversations/${convId}/runs`, { params: { limit: 100 }, signal }).then((r) => {
      const data = unwrap(r);
      if (!Array.isArray(data)) throw new Error('执行列表必须是数组');
      return data.map(normalizeRun).filter((run) => run.visibility === 'conversation');
    }),


  // Bot in conversation
  addToConv: (convId: string, botId: string) =>
    client.post<APIResponse<null>>(`/convs/${convId}/bots`, { bot_id: botId }).then(unwrap),

  removeFromConv: (convId: string, botId: string) =>
    client.delete<APIResponse<null>>(`/convs/${convId}/bots/${botId}`).then(unwrap),

  setParticipation: (convId: string, botId: string, botSettings: string) =>
    client.put<APIResponse<null>>(`/convs/${convId}/bots/${botId}`, { bot_settings: botSettings }).then(unwrap),

  listConvBots: (convId: string): Promise<BotInConv[]> =>
    client.get<APIResponse<{ bots: BotInConv[] }>>(`/convs/${convId}/bots`).then((r) => (r.data.data.bots ?? []).map((bot) => {
      if (bot.owner_type !== 'user' && bot.owner_type !== 'platform') throw new Error('Bot 所有者类型无效');
      if (typeof bot.status !== 'string') throw new Error('Bot 当前状态无效');
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

export function botParticipationEnabled(bot: BotInConv): boolean {
  try {
    const settings: unknown = JSON.parse(bot.bot_settings || '{}');
    return !!settings && typeof settings === 'object' && !Array.isArray(settings)
      && (settings as Record<string, unknown>).enabled !== false;
  } catch { return false; }
}

export function isBotBindingActive(bot: BotInConv | undefined): bot is BotInConv {
  return !!bot && bot.status === 'active' && botParticipationEnabled(bot);
}

// Decode the viewer projection only; accepted input text is not an IM message body.
export function normalizeRun(value: unknown): BotRun {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('执行必须是对象');
  const run = value as Record<string, unknown>;
  const status = run.status;
  const output = run.output_status;
  if (!['accepted', 'running', 'waiting_input', 'waiting_authorization', 'stopping', 'stopped', 'succeeded', 'failed'].includes(String(status))) throw new Error('执行状态无效');
  if (output !== 'none' && output !== 'generated' && output !== 'pending' && output !== 'committed' && output !== 'failed') throw new Error('结果提交状态无效');
  if (run.interaction_status !== 'active' && run.interaction_status !== 'closed') throw new Error('交互状态无效');
  if (!['none', 'requested', 'confirmed', 'rejected', 'unknown'].includes(String(run.stop_status))) throw new Error('停止确认状态无效');
  if (run.stream_status !== 'open' && run.stream_status !== 'closed') throw new Error('输出流状态无效');
  if (!Array.isArray(run.control_user_ids)) throw new Error('显式控制者必须是数组');
  const text = (value: unknown, field: string): string => {
    if (typeof value !== 'string') throw new Error(`${field} 必须是文本`);
    return value;
  };
  if (!Array.isArray(run.steps)) throw new Error('执行阶段必须是数组');
  if (!Array.isArray(run.sources)) throw new Error('执行来源必须是数组');
  if (!Array.isArray(run.input_sources)) throw new Error('输入来源必须是数组');
  if (run.visibility !== 'personal' && run.visibility !== 'conversation') throw new Error('执行可见性无效');
  if (run.visibility === 'conversation' && ((output === 'committed' && !run.message_id) || (status === 'succeeded' && output !== 'committed'))) throw new Error('执行提交结果缺少最终消息确认');
  if (run.executor_type !== 'builtin' && run.executor_type !== 'third_party') throw new Error('执行器类型无效');
  if (!['pending', 'delivered', 'acknowledged'].includes(String(run.delivery_status))) throw new Error('投递状态无效');
  if (!['pending', 'accepted', 'completed', 'stopped', 'stop_rejected'].includes(String(run.executor_status))) throw new Error('执行器接受状态无效');
  const capabilities = run.executor_capabilities;
  if (!capabilities || typeof capabilities !== 'object' || Array.isArray(capabilities)) throw new Error('执行器能力无效');
  const caps = capabilities as Record<string, unknown>;
  if (!Array.isArray(caps.input_types) || !caps.input_types.every((type) => typeof type === 'string')
    || typeof caps.supports_input !== 'boolean' || typeof caps.supports_stop !== 'boolean' || typeof caps.supports_resume !== 'boolean') throw new Error('执行器能力声明无效');
  if (run.result_type != null && run.result_type !== '' && !['text', 'file', 'result'].includes(String(run.result_type))) throw new Error('结果类型无效');
  if (run.result_content != null && (typeof run.result_content !== 'object' || Array.isArray(run.result_content))) throw new Error('结果内容无效');
  if (run.can_control !== undefined && typeof run.can_control !== 'boolean') throw new Error('当前查看者控制资格无效');
  return {
    visibility: run.visibility as BotRun['visibility'],
    personal_user_id: optionalEntityId(run.personal_user_id, 'run.personal_user_id'),
    actions: Array.isArray(run.actions) ? run.actions.map(normalizeAction) : [],
    id: entityId(run.id, 'run.id'), interaction_id: entityId(run.interaction_id, 'run.interaction_id'),
    parent_run_id: optionalEntityId(run.parent_run_id, 'run.parent_run_id'), interaction_status: run.interaction_status,
    bot_id: entityId(run.bot_id, 'run.bot_id'), conv_id: entityId(run.conv_id, 'run.conv_id'),
    binding_id: entityId(run.binding_id, 'run.binding_id'), requester_id: entityId(run.requester_id, 'run.requester_id'),
    controller_id: entityId(run.controller_id, 'run.controller_id'),
    control_user_ids: run.control_user_ids.map((id) => entityId(id, 'run.control_user_id')),
    stop_status: run.stop_status as BotRun['stop_status'], stream_status: run.stream_status,
    stop_reason: text(run.stop_reason, 'run.stop_reason'),
    ...(run.can_control === undefined ? {} : { can_control: run.can_control as boolean }),
    input_key: text(run.input_key, 'run.input_key'), source_message_id: optionalEntityId(run.source_message_id, 'run.source_message_id'),
    input_version: sequence(run.input_version, 'run.input_version', true), version: sequence(run.version, 'run.version', true),
    generation: sequence(run.generation, 'run.generation'),
    input_sources: run.input_sources.map((value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('输入来源格式无效');
      const source = value as Record<string, unknown>;
      if (!Array.isArray(source.message_ids) || !Array.isArray(source.file_ids)) throw new Error('输入引用必须是数组');
      if (source.sender_type !== 'user' && source.sender_type !== 'bot' && source.sender_type !== 'system') throw new Error('补充者类型无效');
      return {
        version: sequence(source.version, 'input_source.version', true), requester_id: entityId(source.requester_id, 'input_source.requester_id'),
        sender_type: source.sender_type, source_message_id: optionalEntityId(source.source_message_id, 'input_source.source_message_id'),
        ...(source.content === undefined ? {} : { content: text(source.content, 'input_source.content') }),
        message_ids: source.message_ids.map((id) => entityId(id, 'input_source.message_id')),
        file_ids: source.file_ids.map((id) => entityId(id, 'input_source.file_id')),
        history_grant_id: optionalEntityId(source.history_grant_id, 'input_source.history_grant_id'),
        parent_run_id: optionalEntityId(source.parent_run_id, 'input_source.parent_run_id'),
      };
    }),
    status: status as BotRun['status'], stage: text(run.stage, 'run.stage'), result_text: text(run.result_text, 'run.result_text'), output_status: output,
    waiting_reason: text(run.waiting_reason, 'run.waiting_reason'),
    context_notice: text(run.context_notice, 'run.context_notice'),
    executor_type: run.executor_type, executor_capabilities: { input_types: caps.input_types, supports_input: caps.supports_input, supports_stop: caps.supports_stop, supports_resume: caps.supports_resume },
    delivery_status: run.delivery_status as BotRun['delivery_status'], executor_status: run.executor_status as BotRun['executor_status'],
    result_type: run.result_type ? run.result_type as BotRun['result_type'] : undefined,
    result_content: run.result_content == null ? undefined : run.result_content as Record<string, unknown>,
    message_id: optionalEntityId(run.message_id, 'run.message_id'), error: run.error == null ? undefined : text(run.error, 'run.error'),
    sources: run.sources.map((value) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('执行来源格式无效');
      const source = value as Record<string, unknown>;
      const identity = {
        kind: source.kind == null ? undefined : text(source.kind, 'source.kind'),
        requester_id: optionalEntityId(source.requester_id, 'source.requester_id'),
        history_grant_id: optionalEntityId(source.history_grant_id, 'source.history_grant_id'),
      };
      if (source.type === 'rag') {
        return { ...identity, type: 'rag' as const, kb_id: entityId(source.kb_id, 'source.kb_id'), doc_id: entityId(source.doc_id, 'source.doc_id'),
          chunk_id: entityId(source.chunk_id, 'source.chunk_id'), kb_name: text(source.kb_name, 'source.kb_name'), title: text(source.title, 'source.title') };
      }
      if (source.type === 'tool') return { ...identity, type: 'tool' as const, tool_name: text(source.tool_name, 'source.tool_name') };
      const kind = source.type;
      const sender = source.sender_type;
      if (kind !== 'request_source' && kind !== 'reference' && kind !== 'history' && kind !== 'file' && kind !== 'parent_run') throw new Error('执行来源类型无效');
      if (sender !== 'user' && sender !== 'bot' && sender !== 'system') throw new Error('执行来源发送者类型无效');
      const senderId = sender === 'system' ? optionalEntityId(source.sender_id === '' ? undefined : source.sender_id, 'source.sender_id') : entityId(source.sender_id, 'source.sender_id');
      if (kind === 'file') return { ...identity, type: kind, file_id: entityId(source.file_id, 'source.file_id'), sender_type: sender, sender_id: senderId };
      if (kind === 'parent_run') return { ...identity, type: kind, parent_run_id: entityId(source.parent_run_id, 'source.parent_run_id'), sender_type: sender, sender_id: senderId };
      return { ...identity, type: kind, message_id: entityId(source.message_id, 'source.message_id'), sender_type: sender, sender_id: senderId };
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
      runs.set(update.id, current && update.can_control === undefined ? { ...update, can_control: current.can_control } : update);
      changed = true;
    } else if (update.can_control !== undefined) {
      // Current access projections can change without changing the shared version.
      const sameVersion = update.version === current.version;
      if (update.can_control !== current.can_control || sameVersion && (
        update.interaction_status !== current.interaction_status || update.stop_reason !== current.stop_reason
        || JSON.stringify(update.actions) !== JSON.stringify(current.actions)
        || update.context_notice !== current.context_notice
        || update.input_sources.length !== current.input_sources.length
        || update.input_sources.some((source, index) => source.version !== current.input_sources[index]?.version || source.content !== current.input_sources[index]?.content)
        || update.stream_status !== current.stream_status || update.control_user_ids.length !== current.control_user_ids.length
        || update.control_user_ids.some((userId, index) => userId !== current.control_user_ids[index]))) {
        runs.set(update.id, sameVersion ? update : { ...current, can_control: update.can_control });
        changed = true;
      }
    }
  }

  if (previous && !changed) return previous;
  return [...runs.values()].sort((a, b) => a.created_at - b.created_at || a.id.localeCompare(b.id));
}
export function normalizeAction(value: unknown): BotRun['actions'][number] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('行动格式无效');
  const action = value as Record<string, unknown>;
  const text = (field: string): string => {
    if (typeof action[field] !== 'string') throw new Error(`行动 ${field} 无效`);
    return action[field] as string;
  };
  if (!action.parameters || typeof action.parameters !== 'object' || Array.isArray(action.parameters) || typeof action.can_approve !== 'boolean') throw new Error('行动参数或批准资格无效');
  return {
    id: entityId(action.id, 'action.id'), run_id: entityId(action.run_id, 'action.run_id'),
    server_id: text('server_id'), tool_name: text('tool_name'), risk: text('risk'), target: text('target'),
    parameters: action.parameters as Record<string, unknown>, status: text('status'), can_approve: action.can_approve,
    parameters_json: text('parameters_json'),
    confirmation_shared: action.confirmation_shared === true, can_share_confirmation: action.can_share_confirmation === true,
    expires_at: sequence(action.expires_at, 'action.expires_at'), generation: sequence(action.generation, 'action.generation'),
    input_version: sequence(action.input_version, 'action.input_version', true),
    ...(typeof action.result === 'string' ? { result: action.result } : {}),
    ...(typeof action.error === 'string' ? { error: action.error } : {}),
  };
}
