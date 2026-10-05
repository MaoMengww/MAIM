import client, { unwrap } from './client';
import type { SendMessageReq, SyncMessagesReq, SearchMessagesReq } from '@/types/api';
import type { APIResponse, Conversation, Message } from '@/types/model';
import { normConv } from './conversation';
import { parseJsonWithExactIntegers } from '@/utils/json';

export interface InboxChange {
  position: string;
  conversation_id: string;
  kind: string;
  message: Message;
}

export interface ConversationSnapshot {
  conversation: Conversation;
  messages: Message[];
}

export interface UserSyncPage {
  next_position: string;
  has_more: boolean;
  rebuild_required: boolean;
  rebuild_reason: string;
  changes: InboxChange[];
  conversations: ConversationSnapshot[];
}

export type { SyncMessagesReq } from '@/types/api';

const MSG_TYPE_KEY: Record<number, string> = {
  1: 'text',
  2: 'image',
  3: 'file',
  4: 'video',
  5: 'audio',
  6: 'location',
  7: 'system',
  8: 'custom',
  9: 'bot',
};
const MSG_CONTENT_KEYS = ['text', 'image', 'file', 'video', 'audio', 'location', 'system', 'custom', 'bot'];
const SYNC_CONTENT_STRINGS: Record<string, string[]> = {
  text: ['text'], image: ['url', 'thumbnail_url', 'format'],
  file: ['url', 'name', 'ext', 'mime_type'], video: ['url', 'thumbnail_url'], audio: ['url'],
  location: ['address', 'name'], system: ['action', 'detail', 'actor_type', 'payload'],
  custom: ['type', 'data'], bot: ['bot_name', 'bot_avatar', 'text', 'raw_payload'],
};
const SYNC_CONTENT_INTEGERS: Record<string, string[]> = {
  image: ['width', 'height', 'size'], file: ['size'], video: ['duration', 'width', 'height', 'size'],
  audio: ['duration', 'size'], bot: ['thinking_time_ms'],
};
const SYNC_CONTENT_ID: Record<string, string> = {
  image: 'file_id', file: 'file_id', video: 'file_id', audio: 'file_id', bot: 'bot_id', system: 'actor_id',
};

function normalizeFlatContent(type: number | undefined, content: any) {
  if (!content || typeof content !== 'object') return content;
  // Bot messages from Kafka have flat fields (text, bot_id, bot_name, etc.)
  // that need wrapping in { bot: ... } for the oneof format.
  if (type === 9 && !content.bot) {
    return { bot: content };
  }
  // Content already uses oneof keys (text/image/file/audio/etc.)
  // but values may be flat strings (from Kafka event) or nested objects (from protobuf HTTP sync).
  if (MSG_CONTENT_KEYS.some((key) => content[key] !== undefined)) {
    if (type === 1 && typeof content.text === 'string') {
      return {
        text: {
          text: content.text,
          mentions: content.mentions ?? content.mention_user_ids ?? [],
          mention_all: content.mention_all ?? false,
        },
      };
    }
    return content;
  }

  switch (type) {
    case 2:
      return {
        image: {
          file_id: content.file_id,
          url: content.url ?? content.image_url,
          thumbnail_url: content.thumbnail_url ?? content.image_thumb ?? content.image_url,
          width: content.width ?? content.image_width ?? 0,
          height: content.height ?? content.image_height ?? 0,
          size: content.size ?? content.file_size ?? 0,
          format: content.format ?? '',
        },
      };
    case 3:
      return {
        file: {
          file_id: content.file_id,
          url: content.url ?? content.file_url,
          name: content.name ?? content.file_name,
          size: content.size ?? content.file_size ?? 0,
          ext: content.ext ?? '',
          mime_type: content.mime_type ?? content.file_mime ?? '',
        },
      };
    case 5:
      return {
        audio: {
          file_id: content.file_id,
          url: content.url ?? content.file_url,
          duration: content.duration ?? 0,
          size: content.size ?? content.file_size ?? 0,
        },
      };
    default: {
      const key = type ? MSG_TYPE_KEY[type] : undefined;
      return key ? { [key]: content } : content;
    }
  }
}

export function normalizeRealtimeMessageContent(msg: any): Message {
  if (!msg) return msg;

  // Keep message_id as string to avoid JS precision loss for int64 IDs

  // Map Kafka event field names → frontend Message field names
  if (msg.conv_id !== undefined && msg.conversation_id === undefined) {
    msg.conversation_id = msg.conv_id;
  }
  if (msg.sender_id !== undefined && msg.from_user_id === undefined) {
    msg.from_user_id = msg.sender_id;
  }
  if (msg.msg_type !== undefined && msg.type === undefined) {
    msg.type = msg.msg_type;
  }
  if (msg.status === undefined) msg.status = 1;

  const type = msg?.type ?? msg?.msg_type;
  if (msg?.content) {
    msg.content = normalizeFlatContent(type, msg.content);
  } else {
    for (const key of MSG_CONTENT_KEYS) {
      if (msg[key] !== undefined) {
        msg.content = { [key]: msg[key] };
        break;
      }
    }
  }
  return msg;
}

function normalizeMessages(data: any): any {
  if (data?.messages) {
    data.messages = data.messages.map(normalizeRealtimeMessageContent);
  }
  return data;
}

function syncRecord(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`同步响应的 ${field} 必须是对象`);
  }
  return value as Record<string, unknown>;
}

function syncDecimal(value: unknown, field: string, positive = false): string {
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) throw new Error(`同步响应的 ${field} 超出安全整数范围`);
    value = String(value);
  }
  if (typeof value !== 'string' || !/^\d+$/.test(value)) {
    throw new Error(`同步响应的 ${field} 必须是十进制整数`);
  }
  const integer = BigInt(value);
  if (integer < (positive ? 1n : 0n) || integer > 9223372036854775807n) {
    throw new Error(`同步响应的 ${field} 超出 int64 范围`);
  }
  return integer.toString();
}

function syncInteger(value: unknown, field: string, positive = false): number {
  const decimal = syncDecimal(value, field, positive);
  if (BigInt(decimal) > BigInt(Number.MAX_SAFE_INTEGER)) {
    throw new Error(`同步响应的 ${field} 超出安全整数范围`);
  }
  return Number(decimal);
}

function syncBoolean(value: unknown, field: string): boolean {
  if (value === undefined) return false;
  if (typeof value !== 'boolean') throw new Error(`同步响应的 ${field} 必须是布尔值`);
  return value;
}

function syncString(value: unknown, field: string): string {
  if (value === undefined) return '';
  if (typeof value !== 'string') throw new Error(`同步响应的 ${field} 必须是字符串`);
  return value;
}

function syncArray(value: unknown, field: string): unknown[] {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error(`同步响应的 ${field} 必须是数组`);
  return value;
}

function syncContent(type: number, value: unknown): Record<string, unknown> {
  const content = syncRecord(value, 'message.content');
  const key = MSG_TYPE_KEY[type];
  if (MSG_CONTENT_KEYS.filter((name) => content[name] !== undefined).length !== 1) {
    throw new Error('同步消息正文必须具有唯一的内容类型');
  }
  const body = { ...syncRecord(content[key], `message.content.${key}`) };
  for (const field of SYNC_CONTENT_STRINGS[key]) body[field] = syncString(body[field], `content.${field}`);
  for (const field of SYNC_CONTENT_INTEGERS[key] ?? []) {
    body[field] = syncInteger(body[field] === undefined ? '0' : body[field], `content.${field}`);
  }
  const idField = SYNC_CONTENT_ID[key];
  if (idField) body[idField] = syncDecimal(body[idField] === undefined ? '0' : body[idField], `content.${idField}`);
  if (key === 'text' || key === 'system') {
    const field = key === 'text' ? 'mention_user_ids' : 'related_user_ids';
    body[field] = syncArray(body[field], `content.${field}`).map((id) => syncDecimal(id, field, true));
  }
  if (key === 'text') body.mention_all = syncBoolean(body.mention_all, 'content.mention_all');
  if (key === 'bot') body.is_streaming = syncBoolean(body.is_streaming, 'content.is_streaming');
  if (key === 'location') {
    for (const field of ['latitude', 'longitude']) {
      const coordinate = body[field] === undefined ? 0 : body[field];
      if (typeof coordinate !== 'number' || !Number.isFinite(coordinate)
        || Math.abs(coordinate) > (field === 'latitude' ? 90 : 180)) {
        throw new Error(`同步消息的 ${field} 无效`);
      }
      body[field] = coordinate;
    }
  }
  return { [key]: body };
}

function syncMessage(value: unknown, conversationId: string): Message {
  const raw = syncRecord(value, 'message');
  const messageId = syncDecimal(raw.message_id, 'message.message_id', true);
  if (syncDecimal(raw.conversation_id, 'message.conversation_id', true) !== conversationId) {
    throw new Error('同步消息的会话 ID 与所属会话不一致');
  }
  const type = syncInteger(raw.type, 'message.type', true);
  if (!MSG_TYPE_KEY[type]) throw new Error('同步消息的类型不支持');
  const status = syncInteger(raw.status === undefined ? 1 : raw.status, 'message.status', true);
  if (status > 4) throw new Error('同步消息的状态无效');
  const topLevelContentKeys = MSG_CONTENT_KEYS.filter((key) => raw[key] !== undefined);
  if (topLevelContentKeys.length > 1 || (raw.content !== undefined && topLevelContentKeys.length !== 0)) {
    throw new Error('同步消息正文包含重复的内容类型');
  }
  const rawContent = raw.content === undefined ? raw : syncRecord(raw.content, 'message.content');
  syncRecord(rawContent[MSG_TYPE_KEY[type]], `message.content.${MSG_TYPE_KEY[type]}`);
  // HTTP protobuf oneof 位于消息顶层；复用实时消息的正文归一化。
  const message = normalizeRealtimeMessageContent({ ...raw, type });
  const content = syncContent(type, message.content);
  const normalized = {
    ...message, message_id: messageId, conversation_id: conversationId,
    seq: syncInteger(raw.seq, 'message.seq', true),
    from_user_id: syncDecimal(raw.from_user_id === undefined ? '0' : raw.from_user_id, 'message.from_user_id'),
    type, status, content,
  };
  for (const field of ['edited_at', 'edit_count', 'created_at', 'updated_at'] as const) {
    normalized[field] = syncInteger(raw[field] === undefined ? '0' : raw[field], `message.${field}`);
  }
  if (raw.client_msg_id !== undefined) normalized.client_msg_id = syncString(raw.client_msg_id, 'message.client_msg_id');
  const replyId = raw.reply_to_id === undefined ? undefined : syncDecimal(raw.reply_to_id, 'message.reply_to_id');
  let reply: Record<string, unknown> | undefined;
  if (raw.reply_to !== undefined) {
    reply = { ...syncRecord(raw.reply_to, 'message.reply_to') };
    reply.message_id = syncDecimal(reply.message_id, 'reply_to.message_id', true);
    if (replyId !== reply.message_id) throw new Error('同步消息的引用 ID 不一致');
    reply.sender_id = syncDecimal(reply.sender_id === undefined ? '0' : reply.sender_id, 'reply_to.sender_id');
    reply.type = syncInteger(reply.type === undefined ? '0' : reply.type, 'reply_to.type');
    if (Number(reply.type) > 9) throw new Error('同步消息的引用类型无效');
    for (const field of ['sender_type', 'sender_name', 'preview']) reply[field] = syncString(reply[field], `reply_to.${field}`);
    reply.deleted = syncBoolean(reply.deleted, 'reply_to.deleted');
  }
  return normalizeRealtimeMessageContent({ ...normalized, reply_to_id: replyId, reply_to: reply });
}

function syncConversation(value: unknown): Conversation {
  const raw = syncRecord(value, 'conversation');
  const conversation = { ...raw, id: syncDecimal(raw.id, 'conversation.id', true),
    type: syncInteger(raw.type, 'conversation.type', true) };
  if (conversation.type < 1 || conversation.type > 3) throw new Error('同步会话类型无效');
  const fields: Record<string, unknown> = conversation;
  for (const field of ['owner_id', 'last_message_id']) {
    fields[field] = syncDecimal(raw[field] === undefined ? '0' : raw[field], `conversation.${field}`);
  }
  for (const field of ['member_count', 'max_seq', 'last_read_seq', 'unread_count', 'created_at', 'updated_at']) {
    fields[field] = syncInteger(raw[field] === undefined ? '0' : raw[field], `conversation.${field}`);
  }
  if (Number(fields.last_read_seq) > Number(fields.max_seq)) throw new Error('同步会话已读序号超出消息范围');
  for (const field of ['is_muted', 'is_pinned', 'is_muted_all']) fields[field] = syncBoolean(raw[field], `conversation.${field}`);
  for (const field of ['name', 'avatar', 'last_message_preview', 'announcement', 'background']) {
    fields[field] = syncString(raw[field], `conversation.${field}`);
  }
  return normConv(conversation);
}

function normalizeUserSyncPage(value: unknown, requestedPosition: string): UserSyncPage {
  const raw = syncRecord(value, 'page');
  const nextPosition = syncDecimal(raw.next_position, 'next_position');
  const hasMore = syncBoolean(raw.has_more, 'has_more');
  const rebuildRequired = syncBoolean(raw.rebuild_required, 'rebuild_required');
  const rebuildReason = syncString(raw.rebuild_reason, 'rebuild_reason');
  const changes = syncArray(raw.changes, 'changes');
  const conversations = syncArray(raw.conversations, 'conversations');
  if (rebuildRequired) {
    if (hasMore || nextPosition === '0' || !rebuildReason || changes.length !== 0) {
      throw new Error('同步重建页必须包含有效位点、原因与完整快照，不能分页或混入增量');
    }
  } else if (BigInt(nextPosition) < BigInt(requestedPosition)
    || (hasMore && BigInt(nextPosition) <= BigInt(requestedPosition))
    || conversations.length !== 0 || rebuildReason !== '') {
    throw new Error('同步增量页的位点或快照状态无效');
  }
  const messageIds = new Set<string>();
  let previousPosition = BigInt(requestedPosition);
  const normalizedChanges = changes.map((value): InboxChange => {
    const change = syncRecord(value, 'change');
    const position = syncDecimal(change.position, 'change.position', true);
    if (BigInt(position) <= previousPosition || BigInt(position) > BigInt(nextPosition)) {
      throw new Error('同步变更位点重复、倒序或超出当前页范围');
    }
    previousPosition = BigInt(position);
    if (change.kind !== 'message.new') throw new Error('同步变更类型不支持');
    const conversationId = syncDecimal(change.conversation_id, 'change.conversation_id', true);
    const message = syncMessage(change.message, conversationId);
    const id = String(message.message_id);
    if (messageIds.has(id)) throw new Error('同步页包含重复消息');
    messageIds.add(id);
    return { position, conversation_id: conversationId, kind: change.kind, message };
  });
  const conversationIds = new Set<string>();
  const normalizedConversations = conversations.map((value): ConversationSnapshot => {
    const snapshot = syncRecord(value, 'snapshot');
    const conversation = syncConversation(snapshot.conversation);
    const conversationId = String(conversation.id);
    if (conversationIds.has(conversationId)) throw new Error('同步重建页包含重复会话');
    conversationIds.add(conversationId);
    let previousSeq = 0;
    const messages = syncArray(snapshot.messages, 'snapshot.messages').map((value) => {
      const message = syncMessage(value, conversationId);
      const id = String(message.message_id);
      if (messageIds.has(id) || message.seq <= previousSeq || message.seq > conversation.max_seq) {
        throw new Error('同步重建消息重复、倒序或超出会话序号范围');
      }
      messageIds.add(id);
      previousSeq = message.seq;
      return message;
    });
    return { conversation, messages };
  });
  return { next_position: nextPosition, has_more: hasMore, rebuild_required: rebuildRequired,
    rebuild_reason: rebuildReason, changes: normalizedChanges, conversations: normalizedConversations };
}

export const msgApi = {
  send: (data: SendMessageReq) =>
    client.post<APIResponse<Message>>('/messages/send', data).then(unwrap),

  sync: async (params: SyncMessagesReq = {}): Promise<UserSyncPage> => {
    const position = syncDecimal(params.position === undefined ? '0' : params.position, 'position');
    if (params.limit !== undefined && (!Number.isSafeInteger(params.limit) || params.limit < 0 || params.limit > 2147483647)) {
      throw new Error('同步请求的 limit 无效');
    }
    const response = await client.get<APIResponse<unknown>>('/messages/sync', {
      params: { position, limit: params.limit },
      // Preserve int64 literals before parsing, and reject malformed JSON before committing a page.
      transformResponse: [(data: unknown): unknown => typeof data === 'string' ? parseJsonWithExactIntegers(data) : data],
    });
    return normalizeUserSyncPage(response.data.data, position);
  },

  getById: (id: number | string) =>
    client.get<APIResponse<{ message: Message }>>(`/messages/${id}`)
      .then((r) => { const d = r.data.data; return normalizeRealtimeMessageContent(d.message ?? d); }),

  recall: (id: number | string) =>
    client.post<APIResponse<null>>(`/messages/${id}/recall`, {}).then(unwrap),

  edit: (id: number | string, text: string) =>
    client.put<APIResponse<any>>(`/messages/${id}`, { text }).then(unwrap),

  delete: (id: number | string) =>
    client.delete<APIResponse<null>>(`/messages/${id}`, { data: {} }).then(unwrap),

  search: (params: SearchMessagesReq) =>
    client.get<APIResponse<{ messages: Message[]; pagination?: any; highlights?: Record<string, string>; type_counts?: { msg_type: number; count: number }[] }>>('/messages/search', { params })
      .then((r) => {
        const data = r.data.data;
        return {
          list: (data.messages ?? []).map(normalizeRealtimeMessageContent),
          total: data.pagination?.total ?? 0,
          page: data.pagination?.page,
          page_size: data.pagination?.page_size,
          highlights: (data.highlights ?? {}) as Record<string, string>,
          type_counts: (data.type_counts ?? []) as { msg_type: number; count: number }[],
        };
      }),



  getAroundSeq: (convId: number, seq: number) =>
    client.get<APIResponse<{ messages: Message[] }>>(`/messages/${convId}/around/${seq}`)
      .then((r) => (normalizeMessages(r.data.data)?.messages ?? [])),
};
