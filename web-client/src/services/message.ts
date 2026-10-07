import client, { unwrap } from './client';
import type { SendMessageReq, SyncMessagesReq, SearchMessagesReq } from '@/types/api';
import type { APIResponse, Conversation, Message, MsgContentOneof, MsgType, ReplySummary } from '@/types/model';
import { normConv } from './conversation';
import { entityId, optionalEntityId, sequence, quantity, knowledgeSources } from '@/utils/json';

interface InboxChangeBase {
  position: number;
  conversation_id: string;
}

export type InboxChange = InboxChangeBase & (
  | { kind: 'message.new' | 'message.edited' | 'message.recalled'; message: Message; conversation?: Conversation }
  | { kind: 'message.deleted'; message_id: string; conversation?: Conversation }
  | { kind: 'conversation.upsert'; conversation: Conversation }
  | { kind: 'conversation.removed' }
  | { kind: 'read.updated'; last_read_seq: number; conversation: Conversation }
);

export interface ConversationSnapshot {
  conversation: Conversation;
  messages: Message[];
}

export interface UserSyncPage {
  next_position: number;
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

// WS events have a defined flat content schema; HTTP protobuf oneofs are decoded separately.
export function normalizeRealtimeMessageContent(value: unknown): Message {
  const event = syncRecord(value, 'event');
  const type = sequence(event.msg_type, 'event.msg_type', true);
  const body = syncRecord(event.content, 'event.content');
  const key = MSG_TYPE_KEY[type];
  if (!key) throw new Error('实时消息类型无效');
  const content = { ...body };
  if (type === 1) content.mention_user_ids = body.mentions;
  if (type === 2) Object.assign(content, { url: body.image_url, thumbnail_url: body.image_thumb,
    width: body.image_width, height: body.image_height, size: body.file_size });
  if (type === 3) Object.assign(content, { url: body.file_url, name: body.file_name,
    size: body.file_size, mime_type: body.file_mime });
  if (type === 5) Object.assign(content, { url: body.file_url, size: body.file_size });
  return syncMessage({ ...event, conversation_id: event.conv_id, from_user_id: event.sender_id,
    type, reply_to_id: event.reply_to_msg_id, content: { [key]: content } }, entityId(event.conv_id, 'event.conv_id'));
}

function normalizeHttpMessage(value: unknown): Message {
  const message = syncRecord(value, 'message');
  return syncMessage(message, entityId(message.conversation_id, 'message.conversation_id'));
}

function normalizeMessages(data: { messages?: unknown[] }): Message[] {
  return (data.messages ?? []).map(normalizeHttpMessage);
}

function syncRecord(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`同步响应的 ${field} 必须是对象`);
  }
  return value as Record<string, unknown>;
}

function syncInteger(value: unknown, field: string, positive = false): number {
  return sequence(value, field, positive);
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
    body[field] = quantity(body[field], `content.${field}`);
  }
  const idField = SYNC_CONTENT_ID[key];
  if (idField) body[idField] = key === 'system'
    ? optionalEntityId(body[idField], `content.${idField}`) : entityId(body[idField], `content.${idField}`);
  if (key === 'text' || key === 'system') {
    const field = key === 'text' ? 'mention_user_ids' : 'related_user_ids';
    body[field] = syncArray(body[field], `content.${field}`).map((id) => entityId(id, field));
  }
  if (key === 'text') body.mention_all = syncBoolean(body.mention_all, 'content.mention_all');
  if (key === 'bot') {
    body.is_streaming = syncBoolean(body.is_streaming, 'content.is_streaming');
    if (typeof body.raw_payload === 'string' && body.raw_payload) knowledgeSources(JSON.parse(body.raw_payload).kb_sources);
  }
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
  const messageId = entityId(raw.message_id, 'message.message_id');
  if (entityId(raw.conversation_id, 'message.conversation_id') !== conversationId) {
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
  const content = syncContent(type, { [MSG_TYPE_KEY[type]]: rawContent[MSG_TYPE_KEY[type]] });
  const normalized: Message = {
    message_id: messageId, conversation_id: conversationId,
    seq: syncInteger(raw.seq, 'message.seq', true),
    from_user_id: optionalEntityId(raw.from_user_id, 'message.from_user_id'),
    type: type as MsgType, status, content: content as unknown as MsgContentOneof,
    edited_at: quantity(raw.edited_at, 'message.edited_at'), edit_count: quantity(raw.edit_count, 'message.edit_count'),
    created_at: quantity(raw.created_at, 'message.created_at'), updated_at: quantity(raw.updated_at, 'message.updated_at'),
  };
  const clientMsgId = optionalEntityId(raw.client_msg_id, 'message.client_msg_id');
  const replyId = optionalEntityId(raw.reply_to_id, 'message.reply_to_id');
  let reply: ReplySummary | undefined;
  if (raw.reply_to !== undefined && raw.reply_to !== null) {
    const summary = syncRecord(raw.reply_to, 'message.reply_to');
    const replyMessageId = entityId(summary.message_id, 'reply_to.message_id');
    if (replyId !== replyMessageId) throw new Error('同步消息的引用 ID 不一致');
    const replyType = syncInteger(summary.type ?? 0, 'reply_to.type');
    if (replyType > 9) throw new Error('同步消息的引用类型无效');
    reply = { message_id: replyMessageId, sender_id: optionalEntityId(summary.sender_id, 'reply_to.sender_id'),
      type: replyType as MsgType | 0, sender_type: syncString(summary.sender_type, 'reply_to.sender_type'),
      sender_name: syncString(summary.sender_name, 'reply_to.sender_name'), preview: syncString(summary.preview, 'reply_to.preview'),
      deleted: syncBoolean(summary.deleted, 'reply_to.deleted') };
  }
  return { ...normalized, client_msg_id: clientMsgId, reply_to_id: replyId, reply_to: reply };
}

function syncConversation(value: unknown): Conversation {
  const raw = syncRecord(value, 'conversation');
  const conversation = { ...raw, id: entityId(raw.id, 'conversation.id'),
    type: syncInteger(raw.type, 'conversation.type', true) };
  if (conversation.type < 1 || conversation.type > 3) throw new Error('同步会话类型无效');
  const fields: Record<string, unknown> = conversation;
  for (const field of ['owner_id', 'last_message_id']) {
    fields[field] = optionalEntityId(raw[field], `conversation.${field}`);
  }
  for (const field of ['member_count', 'max_seq', 'last_read_seq', 'unread_count', 'created_at', 'updated_at']) {
    fields[field] = field.endsWith('_at') ? quantity(raw[field], `conversation.${field}`)
      : syncInteger(raw[field] ?? 0, `conversation.${field}`);
  }
  for (const field of ['is_muted', 'is_pinned', 'is_muted_all']) fields[field] = syncBoolean(raw[field], `conversation.${field}`);
  for (const field of ['name', 'avatar', 'last_message_preview', 'announcement', 'background']) {
    fields[field] = syncString(raw[field], `conversation.${field}`);
  }
  return normConv(conversation);
}

function normalizeUserSyncPage(value: unknown, requestedPosition: number): UserSyncPage {
  const raw = syncRecord(value, 'page');
  const nextPosition = syncInteger(raw.next_position, 'next_position');
  const hasMore = syncBoolean(raw.has_more, 'has_more');
  const rebuildRequired = syncBoolean(raw.rebuild_required, 'rebuild_required');
  const rebuildReason = syncString(raw.rebuild_reason, 'rebuild_reason');
  const changes = syncArray(raw.changes, 'changes');
  const conversations = syncArray(raw.conversations, 'conversations');
  if (rebuildRequired) {
    if (hasMore || nextPosition === 0 || !rebuildReason || changes.length !== 0) {
      throw new Error('同步重建页必须包含有效位点、原因与完整快照，不能分页或混入增量');
    }
  } else if (nextPosition < requestedPosition
    || (hasMore && nextPosition <= requestedPosition)
    || conversations.length !== 0 || rebuildReason !== '') {
    throw new Error('同步增量页的位点或快照状态无效');
  }
  const messageIds = new Set<string>();
  let previousPosition = requestedPosition;
  const normalizedChanges = changes.map((value): InboxChange => {
    const change = syncRecord(value, 'change');
    const position = syncInteger(change.position, 'change.position', true);
    if (position <= previousPosition || position > nextPosition) {
      throw new Error('同步变更位点重复、倒序或超出当前页范围');
    }
    previousPosition = position;
    const conversationId = entityId(change.conversation_id, 'change.conversation_id');
    const base = { position, conversation_id: conversationId };
    const conversation = change.conversation === undefined ? undefined : syncConversation(change.conversation);
    if (conversation && conversation.id !== conversationId) throw new Error('同步会话 ID 与变更不一致');
    switch (change.kind) {
      case 'message.new':
      case 'message.edited':
      case 'message.recalled':
        return { ...base, kind: change.kind, message: syncMessage(change.message, conversationId), conversation };
      case 'message.deleted':
        return { ...base, kind: change.kind, message_id: entityId(change.message_id, 'change.message_id'), conversation };
      case 'conversation.upsert':
        if (!conversation) throw new Error('同步会话变更缺少完整快照');
        return { ...base, kind: change.kind, conversation };
      case 'conversation.removed':
        return { ...base, kind: change.kind };
      case 'read.updated': {
        const lastReadSeq = syncInteger(change.last_read_seq === undefined ? 0 : change.last_read_seq, 'change.last_read_seq');
        if (!conversation || lastReadSeq !== conversation.last_read_seq) throw new Error('同步已读位点与会话不一致');
        return { ...base, kind: change.kind, last_read_seq: lastReadSeq, conversation };
      }
      default:
        throw new Error('同步变更类型不支持');
    }
  });
  const conversationIds = new Set<string>();
  const normalizedConversations = conversations.map((value): ConversationSnapshot => {
    const snapshot = syncRecord(value, 'snapshot');
    const conversation = syncConversation(snapshot.conversation);
    const conversationId = conversation.id;
    if (conversationIds.has(conversationId)) throw new Error('同步重建页包含重复会话');
    conversationIds.add(conversationId);
    let previousSeq = 0;
    const messages = syncArray(snapshot.messages, 'snapshot.messages').map((value) => {
      const message = syncMessage(value, conversationId);
      const id = message.message_id;
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
  send: async (data: SendMessageReq): Promise<Message> => {
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(data.client_msg_id)) throw new Error('提交键必须是 UUIDv4');
    const ack = syncRecord(await client.post<APIResponse<unknown>>('/messages/send', data).then(unwrap), 'ack');
    const type = syncInteger(ack.type, 'ack.type', true);
    const body = syncRecord(ack.content, 'ack.content');
    if (type === 1) return normalizeRealtimeMessageContent({ message_id: ack.message_id, conv_id: ack.conv_id,
      sender_id: ack.from_user_id, seq: ack.seq, created_at: ack.created_at, reply_to_msg_id: ack.reply_to_msg_id,
      msg_type: type, content: body, client_msg_id: data.client_msg_id });
    const file = syncRecord(syncArray(body.files, 'ack.content.files')[0], 'ack.content.files[0]');
    return normalizeHttpMessage({ message_id: ack.message_id, conversation_id: ack.conv_id,
      from_user_id: ack.from_user_id, seq: ack.seq, created_at: ack.created_at, reply_to_id: ack.reply_to_msg_id,
      type, client_msg_id: data.client_msg_id, content: { [MSG_TYPE_KEY[type]]: { ...file,
        name: file.file_name, thumbnail_url: type === 2 ? file.url : undefined, mime_type: file.mime_type } } });
  },

  history: (convId: string, cursor = 0, limit = 50) =>
    client.get<APIResponse<{ messages: unknown[]; pagination: { next_cursor: number; has_more: boolean } }>>(`/convs/${convId}/messages`,
      { params: { cursor: sequence(cursor, 'history.cursor'), limit } }).then((r) => ({
        messages: normalizeMessages(r.data.data),
        nextCursor: sequence(r.data.data.pagination.next_cursor, 'history.next_cursor'),
        hasMore: r.data.data.pagination.has_more,
      })),
  sync: async (params: SyncMessagesReq = {}): Promise<UserSyncPage> => {
    const position = syncInteger(params.position ?? 0, 'position');
    if (params.limit !== undefined && (!Number.isSafeInteger(params.limit) || params.limit < 0 || params.limit > 2147483647)) {
      throw new Error('同步请求的 limit 无效');
    }
    const response = await client.get<APIResponse<unknown>>('/messages/sync', {
      params: { position, limit: params.limit },
      transformResponse: [(data: unknown): unknown => typeof data === 'string' ? JSON.parse(data) : data],
    });
    return normalizeUserSyncPage(response.data.data, position);
  },

  getById: (id: string) =>
    client.get<APIResponse<{ message: Message }>>(`/messages/${id}`)
      .then((r) => normalizeHttpMessage(r.data.data.message)),

  recall: (id: string) =>
    client.post<APIResponse<null>>(`/messages/${id}/recall`, {}).then(unwrap),

  edit: (id: string, text: string) =>
    client.put<APIResponse<unknown>>(`/messages/${id}`, { text }).then(unwrap),

  delete: (id: string, deleteForAll = false) =>
    client.delete<APIResponse<null>>(`/messages/${id}`, { data: { delete_for_all: deleteForAll } }).then(unwrap),
  search: (params: SearchMessagesReq) =>
    client.get<APIResponse<{ messages: unknown[]; pagination?: { total: number; page: number; page_size: number }; highlights?: Record<string, string>; type_counts?: { msg_type: number; count: number }[] }>>('/messages/search', { params })
      .then((r) => {
        const data = r.data.data;
        return {
          list: (data.messages ?? []).map(normalizeHttpMessage),
          total: data.pagination?.total ?? 0,
          page: data.pagination?.page,
          page_size: data.pagination?.page_size,
          highlights: (data.highlights ?? {}) as Record<string, string>,
          type_counts: (data.type_counts ?? []) as { msg_type: number; count: number }[],
        };
      }),



  getAroundSeq: (convId: string, seq: number) =>
    client.get<APIResponse<{ messages: unknown[] }>>(`/messages/${convId}/around/${sequence(seq, 'seq', true)}`)
      .then((r) => normalizeMessages(r.data.data)),
};
