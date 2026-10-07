import client, { unwrap } from './client';
import type { CreateConvReq, UpdateConvReq } from '@/types/api';
import type { APIResponse, Conversation, ConvType, ConvMember } from '@/types/model';
import { entityId, optionalEntityId, quantity, sequence } from '@/utils/json';

export function normConv(value: unknown): Conversation {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('会话必须是对象');
  const c = value as Record<string, unknown>;
  const types: Record<number, ConvType> = { 1: 'private', 2: 'group', 3: 'system' };
  const type = sequence(c.type, 'conversation.type', true);
  if (!types[type]) throw new Error('会话类型无效');
  const maxSeq = sequence(c.max_seq ?? 0, 'conversation.max_seq');
  const lastReadSeq = sequence(c.last_read_seq ?? 0, 'conversation.last_read_seq');
  if (lastReadSeq > maxSeq) throw new Error('已读序号超出会话范围');
  return { id: entityId(c.id, 'conversation.id'), type: types[type],
    name: typeof c.name === 'string' ? c.name : '', avatar: typeof c.avatar === 'string' ? c.avatar : '',
    member_count: sequence(c.member_count ?? 0, 'conversation.member_count'),
    unread_count: sequence(c.unread_count ?? 0, 'conversation.unread_count'),
    last_message_preview: typeof c.last_message_preview === 'string' ? c.last_message_preview : '',
    is_muted: c.is_muted === true, is_pinned: c.is_pinned === true, is_muted_all: c.is_muted_all === true,
    announcement: typeof c.announcement === 'string' ? c.announcement : '', background: typeof c.background === 'string' ? c.background : '',
    owner_id: optionalEntityId(c.owner_id, 'conversation.owner_id'),
    peer_user_id: optionalEntityId(c.peer_user_id, 'conversation.peer_user_id'),
    last_message_id: optionalEntityId(c.last_message_id, 'conversation.last_message_id'),
    max_seq: maxSeq, last_read_seq: lastReadSeq,
    created_at: quantity(c.created_at, 'conversation.created_at'), updated_at: quantity(c.updated_at, 'conversation.updated_at') };
}

export const convApi = {
  create: (data: CreateConvReq) =>
    client.post<APIResponse<{ conversation_id: string; conversation: unknown }>>('/convs', data)
      .then((r) => ({ id: entityId(r.data.data.conversation_id, 'conversation_id'), conversation: normConv(r.data.data.conversation) })),
  get: (id: string) => client.get<APIResponse<{ conversation: unknown }>>(`/convs/${id}`).then((r) => normConv(r.data.data.conversation)),
  list: (params?: { limit?: number; cursor?: string }) =>
    client.get<APIResponse<{ conversations: unknown[]; pagination?: { total?: number; next_cursor?: string; has_more?: boolean } }>>('/convs', { params }).then((r) => ({
      list: (r.data.data.conversations ?? []).map(normConv), total: r.data.data.pagination?.total ?? 0,
      next_cursor: r.data.data.pagination?.next_cursor, has_more: r.data.data.pagination?.has_more,
    })),
  update: (id: string, data: UpdateConvReq) => client.put<APIResponse<null>>(`/convs/${id}/info`, data).then(unwrap),
  delete: (id: string) => client.delete<APIResponse<null>>(`/convs/${id}`).then(unwrap),
  getMembers: (id: string) => client.get<APIResponse<{ members: (Omit<ConvMember, 'member_type'> & { member_type: number })[] }>>(`/convs/${id}/members`).then((r) =>
    (r.data.data.members ?? []).map((m) => {
      const userId = optionalEntityId(m.user_id, 'member.user_id');
      const botId = optionalEntityId(m.bot_id, 'member.bot_id');
      if (!!userId === !!botId) throw new Error('成员必须且只能引用用户或 Bot');
      if (m.member_type !== 1 && m.member_type !== 2 || m.member_type === 1 && !userId || m.member_type === 2 && !botId) throw new Error('成员类型与引用不一致');
      return { ...m, user_id: userId, bot_id: botId, member_type: m.member_type === 2 ? 'bot' as const : 'user' as const,
        last_read_seq: sequence(m.last_read_seq ?? 0, 'member.last_read_seq'),
        joined_at: quantity(m.joined_at, 'member.joined_at'), mute_until: quantity(m.mute_until, 'member.mute_until') };
    })),
  addMembers: (id: string, memberIds: string[]) => client.post<APIResponse<null>>(`/convs/${id}/members/invite`, { user_ids: memberIds }).then(unwrap),
  removeMembers: (id: string, memberIds: string[]) => client.post<APIResponse<null>>(`/convs/${id}/members/kick`, { user_ids: memberIds }).then(unwrap),
  updateMember: (convId: string, userId: string, data: { role?: number; alias?: string }) => client.put<APIResponse<null>>(`/convs/${convId}/members/${userId}/role`, data).then(unwrap),
  muteAll: (id: string) => client.post<APIResponse<null>>(`/convs/${id}/mute_all`).then(unwrap),
  unmuteAll: (id: string) => client.delete<APIResponse<null>>(`/convs/${id}/mute_all`).then(unwrap),
  muteMember: (convId: string, userId: string, durationSeconds?: number) => client.put<APIResponse<null>>(`/convs/${convId}/members/${userId}/mute`, { duration_seconds: durationSeconds ?? 0 }).then(unwrap),
  unmuteMember: (convId: string, userId: string) => client.delete<APIResponse<null>>(`/convs/${convId}/members/${userId}/mute`).then(unwrap),
  setAnnouncement: (id: string, content: string) => client.put<APIResponse<null>>(`/convs/${id}/announcement`, { content }).then(unwrap),
  deleteAnnouncement: (id: string) => client.delete<APIResponse<null>>(`/convs/${id}/announcement`).then(unwrap),
  transferOwner: (id: string, newOwnerId: string) => client.post<APIResponse<null>>(`/convs/${id}/transfer`, { new_owner_id: newOwnerId }).then(unwrap),
  markRead: (id: string, seq: number) => client.put<APIResponse<null>>(`/convs/${id}/read`, { seq: sequence(seq, 'read.seq') }).then(unwrap),
  getReadStatus: (id: string, messageId: string) => client.get<APIResponse<{ read_count: number; total_count: number; read_users: { user_id: string; read_at: number }[] }>>(`/convs/${id}/read_status/${messageId}`).then(unwrap),
  getSettings: (id: string) => client.get<APIResponse<Record<string, unknown>>>(`/convs/${id}/settings`).then(unwrap),
  updateSettings: (id: string, data: Record<string, unknown>) => client.put<APIResponse<null>>(`/convs/${id}/settings`, data).then(unwrap),
  uploadAvatar: (convId: string, file: File) => {
    const fd = new FormData();
    fd.append('file', file);
    return client.post<APIResponse<{ avatar: string }>>(`/convs/${convId}/avatar`, fd).then(unwrap);
  },
};
