import client, { unwrap } from './client';
import type { APIResponse, Notification } from '@/types/model';

export const notifApi = {
  list: (params?: { page?: number; page_size?: number; type?: number; is_read?: boolean }) =>
    client.get<APIResponse<{ notifications: Notification[]; pagination: { total: string } }>>('/notifications', { params })
      .then((r) => ({ list: r.data.data.notifications, total: Number(r.data.data.pagination.total) })),

  unreadCount: () =>
    client.get<APIResponse<{ count: number }>>('/notifications/unread_count').then((response) => response.data.data.count),

  markRead: (id: string) =>
    client.post<APIResponse<null>>(`/notifications/${id}/read`).then(unwrap),

  markAllRead: () =>
    client.post<APIResponse<null>>('/notifications/read_all').then(unwrap),

  delete: (id: string) =>
    client.delete<APIResponse<null>>(`/notifications/${id}`).then(unwrap),
};
