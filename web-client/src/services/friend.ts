import client, { unwrap } from './client';
import type { SendFriendRequestReq, SetRemarkReq, SetGroupReq, CreateFriendGroupReq } from '@/types/api';
import type {
  APIResponse, FriendInfo, FriendRequest, FriendGroup,
} from '@/types/model';

export const friendApi = {
  list: (params?: { group_id?: string }) =>
    client.get<APIResponse<{ friends: FriendInfo[]; pagination?: any }>>('/friends', { params })
      .then((r) => ({ list: r.data.data.friends ?? [], total: r.data.data.pagination?.total ?? 0 })),

  sendRequest: (data: SendFriendRequestReq) =>
    client.post<APIResponse<null>>('/friends/requests', data).then(unwrap),

  acceptRequest: (id: string) =>
    client.post<APIResponse<null>>(`/friends/requests/${id}/accept`).then(unwrap),

  rejectRequest: (id: string) =>
    client.post<APIResponse<null>>(`/friends/requests/${id}/reject`).then(unwrap),

  cancelRequest: (id: string) =>
    client.delete<APIResponse<null>>(`/friends/requests/${id}`).then(unwrap),

  pendingRequests: () =>
    client.get<APIResponse<{ requests: FriendRequest[] }>>('/friends/requests/pending')
      .then((r) => r.data.data.requests ?? []),

  sentRequests: () =>
    client.get<APIResponse<{ requests: FriendRequest[] }>>('/friends/requests/sent')
      .then((r) => r.data.data.requests ?? []),

  deleteFriend: (userId: string) =>
    client.delete<APIResponse<null>>(`/friends/${userId}`).then(unwrap),

  setRemark: (userId: string, data: SetRemarkReq) =>
    client.put<APIResponse<null>>(`/friends/${userId}/remark`, data).then(unwrap),

  setGroup: (userId: string, data: SetGroupReq) =>
    client.put<APIResponse<null>>(`/friends/${userId}/group`, data).then(unwrap),

  createGroup: (data: CreateFriendGroupReq) =>
    client.post<APIResponse<FriendGroup>>('/friends/groups', data).then(unwrap),

  renameGroup: (id: string, name: string) =>
    client.put<APIResponse<null>>(`/friends/groups/${id}`, { name }).then(unwrap),

  deleteGroup: (id: string) =>
    client.delete<APIResponse<null>>(`/friends/groups/${id}`).then(unwrap),

  listGroups: () =>
    client.get<APIResponse<{ groups: FriendGroup[] }>>('/friends/groups')
      .then((r) => r.data.data.groups ?? []),

  blockUser: (userId: string) =>
    client.post<APIResponse<null>>(`/friends/${userId}/block`).then(unwrap),

  unblockUser: (userId: string) =>
    client.delete<APIResponse<null>>(`/friends/${userId}/block`).then(unwrap),

  blacklist: () =>
    client.get<APIResponse<{ users: any[] }>>('/friends/blacklist')
      .then((r) => r.data.data.users ?? []),
};
