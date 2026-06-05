import client, { unwrap } from './client';
import type { CreateConvReq, UpdateConvReq } from '@/types/api';
import type {
  APIResponse, Conversation, ConvMember, PageData,
} from '@/types/model';

/** Normalize proto ConversationType enum => 'private' | 'group' */
function normType(t: number): 'private' | 'group' {
  return t === 2 ? 'group' : 'private';
}

function normConv(c: any): Conversation {
  if (!c) return c;
  return { ...c, type: normType(c.type) };
}

function normConvList(list: any[]): Conversation[] {
  return (list || []).map(normConv);
}

export const convApi = {
  create: (data: CreateConvReq) =>
    client.post<APIResponse<{ conversation_id: number; conversation: any }>>('/convs', data)
      .then((r) => ({ id: r.data.data.conversation_id, conversation: normConv(r.data.data.conversation) })),

  get: (id: number) =>
    client.get<APIResponse<{ conversation: any }>>(`/convs/${id}`)
      .then((r) => normConv(r.data.data.conversation)),

  list: (params?: { limit?: number; cursor?: string }) =>
    client.get<APIResponse<{ conversations: any[]; pagination?: any }>>('/convs', { params })
      .then((r) => ({
        list: normConvList(r.data.data.conversations),
        total: r.data.data.pagination?.total ?? 0,
        next_cursor: r.data.data.pagination?.next_cursor,
        has_more: r.data.data.pagination?.has_more,
      })),

  update: (id: number, data: UpdateConvReq) =>
    client.put<APIResponse<null>>(`/convs/${id}/info`, data).then(unwrap),

  delete: (id: number) =>
    client.delete<APIResponse<null>>(`/convs/${id}`).then(unwrap),

  getMembers: (id: number) =>
    client.get<APIResponse<{ members: ConvMember[] }>>(`/convs/${id}/members`)
      .then((r) => r.data.data.members),

  addMembers: (id: number, memberIds: number[]) =>
    client.post<APIResponse<null>>(`/convs/${id}/members/invite`, { user_ids: memberIds }).then(unwrap),

  removeMembers: (id: number, memberIds: number[]) =>
    client.post<APIResponse<null>>(`/convs/${id}/members/kick`, { user_ids: memberIds }).then(unwrap),

  updateMember: (convId: number, userId: number, data: { role?: string; alias?: string }) =>
    client.put<APIResponse<null>>(`/convs/${convId}/members/${userId}/role`, data).then(unwrap),

  muteAll: (id: number) =>
    client.post<APIResponse<null>>(`/convs/${id}/mute_all`).then(unwrap),

  unmuteAll: (id: number) =>
    client.delete<APIResponse<null>>(`/convs/${id}/mute_all`).then(unwrap),

  setAnnouncement: (id: number, content: string) =>
    client.put<APIResponse<null>>(`/convs/${id}/announcement`, { content }).then(unwrap),

  deleteAnnouncement: (id: number) =>
    client.delete<APIResponse<null>>(`/convs/${id}/announcement`).then(unwrap),

  transferOwner: (id: number, newOwnerId: number) =>
    client.post<APIResponse<null>>(`/convs/${id}/transfer`, { new_owner_id: newOwnerId }).then(unwrap),

  markRead: (id: number, seq: number) =>
    client.put<APIResponse<null>>(`/convs/${id}/read`, { seq }).then(unwrap),

  getReadStatus: (id: number, messageId: number) =>
    client.get<APIResponse<{ read_count: number; total_count: number; read_users: { user_id: number; read_at: number }[] }>>(`/convs/${id}/read_status/${messageId}`).then(unwrap),

  getSettings: (id: number) =>
    client.get<APIResponse<any>>(`/convs/${id}/settings`).then(unwrap),

  updateSettings: (id: number, data: Record<string, unknown>) =>
    client.put<APIResponse<null>>(`/convs/${id}/settings`, data).then(unwrap),
};
