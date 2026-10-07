import { useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { notifApi } from '@/services/notification';
import { wsOn } from '@/services/ws';
import { useAuthStore } from '@/stores/auth';

export function useNotifications() {
  const queryClient = useQueryClient();
  const accountId = useAuthStore((state) => state.user?.id);

  const listQuery = useQuery({
    queryKey: ['notifications', accountId, 'list'],
    queryFn: () => notifApi.list({ page: 1, page_size: 20 }),
    enabled: !!accountId,
  });

  const unreadCountQuery = useQuery({
    queryKey: ['notifications', accountId, 'unread-count'],
    queryFn: () => notifApi.unreadCount(),
    enabled: !!accountId,
  });

  useEffect(() => {
    const unsub = wsOn('notification.new', () => {
      queryClient.invalidateQueries({ queryKey: ['notifications'] });
    });
    return unsub;
  }, [queryClient]);

  const markReadMutation = useMutation({
    mutationFn: (id: string) => notifApi.markRead(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
  });

  const markAllReadMutation = useMutation({
    mutationFn: () => notifApi.markAllRead(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
  });

  return { listQuery, unreadCountQuery, markReadMutation, markAllReadMutation };
}
