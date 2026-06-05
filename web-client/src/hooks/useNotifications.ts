import { useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { notifApi } from '@/services/notification';
import { wsOn } from '@/services/ws';

export function useNotifications() {
  const queryClient = useQueryClient();

  const listQuery = useQuery({
    queryKey: ['notifications'],
    queryFn: () => notifApi.list(),
  });

  useEffect(() => {
    const unsub = wsOn('notification.new', () => {
      queryClient.invalidateQueries({ queryKey: ['notifications'] });
    });
    return unsub;
  }, [queryClient]);

  const markReadMutation = useMutation({
    mutationFn: (id: number) => notifApi.markRead(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
  });

  const markAllReadMutation = useMutation({
    mutationFn: () => notifApi.markAllRead(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notifications'] }),
  });

  return { listQuery, markReadMutation, markAllReadMutation };
}
