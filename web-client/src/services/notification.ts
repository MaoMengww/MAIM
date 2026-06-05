import client, { unwrap } from './client';
import type { APIResponse, Notification, PageData } from '@/types/model';

export const notifApi = {
  list: () =>
    client.get<APIResponse<{ list: Notification[]; total: number }>>('/notifications')
      .then((r) => ({ list: r.data.data.list ?? [], total: r.data.data.total ?? 0 })),

  markRead: (id: number) =>
    client.post<APIResponse<null>>(`/notifications/${id}/read`).then(unwrap),

  markAllRead: () =>
    client.post<APIResponse<null>>('/notifications/read_all').then(unwrap),

  delete: (id: number) =>
    client.delete<APIResponse<null>>(`/notifications/${id}`).then(unwrap),
};
