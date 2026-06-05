import client, { unwrap } from './client';
import type { SendMessageReq, SyncMessagesReq, SearchMessagesReq } from '@/types/api';
import type { APIResponse, Message } from '@/types/model';

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

  // Normalize message_id to number so dedup comparisons work across sync (string) and WS (number)
  if (msg.message_id !== undefined) {
    msg.message_id = Number(msg.message_id);
  }

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

export const msgApi = {
  send: (data: SendMessageReq) =>
    client.post<APIResponse<Message>>('/messages/send', data).then(unwrap),

  sync: (params: SyncMessagesReq) =>
    client.get<APIResponse<{ messages: Message[]; max_seq: number; has_more: boolean }>>(
      `/messages/${params.conversation_id}/sync`, { params },
    ).then((r) => normalizeMessages(r.data.data)),

  getById: (id: number) =>
    client.get<APIResponse<{ message: Message }>>(`/messages/${id}`)
      .then((r) => { const d = r.data.data; return normalizeRealtimeMessageContent(d.message ?? d); }),

  recall: (id: number) =>
    client.post<APIResponse<null>>(`/messages/${id}/recall`, {}).then(unwrap),

  edit: (id: number, text: string) =>
    client.put<APIResponse<any>>(`/messages/${id}`, { text }).then(unwrap),

  delete: (id: number) =>
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

  forward: (messageIds: number[], targetConvId: number) =>
    client.post<APIResponse<any>>('/messages/forward', {
      message_ids: messageIds,
      target_conversation_id: targetConvId,
    }).then(unwrap),

  getAroundSeq: (convId: number, seq: number) =>
    client.get<APIResponse<{ messages: Message[] }>>(`/messages/${convId}/around/${seq}`)
      .then((r) => (normalizeMessages(r.data.data)?.messages ?? [])),
};
