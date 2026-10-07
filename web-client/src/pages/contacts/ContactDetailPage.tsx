import { useState, useEffect, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Descriptions, Spin, message, Modal, Select, Input } from 'antd';
import { Avatar } from '@/components/common/Avatar';
import { PresenceDot } from '@/components/common/PresenceDot';
import client from '@/services/client';
import { convApi } from '@/services/conversation';
import { friendApi } from '@/services/friend';
import { wsSend, wsOn } from '@/services/ws';
import { useWSStore } from '@/stores/ws';
import type { FriendInfo, FriendGroup } from '@/types/model';

const descProps = {
  column: 1 as const,
  bordered: true,
  size: 'small' as const,
  styles: {
    label: { color: 'var(--aim-text-secondary)', background: 'var(--aim-surface)', width: 100 },
    content: { color: 'var(--aim-text)' },
  },
};

export function ContactDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [sending, setSending] = useState(false);
  const [online, setOnline] = useState(false);
  const [editingRemark, setEditingRemark] = useState(false);
  const [remarkValue, setRemarkValue] = useState('');
  const [savingRemark, setSavingRemark] = useState(false);
  const remarkInputRef = useRef<any>(null);
  const wsStatus = useWSStore((s) => s.status);

  useEffect(() => {
    if (editingRemark && remarkInputRef.current) {
      remarkInputRef.current.focus();
    }
  }, [editingRemark]);

  const handleRemarkSave = async () => {
    if (savingRemark || !id) return;
    const trimmed = remarkValue.trim();
    const current = friendInfo?.remark || '';
    if (trimmed === current) {
      setEditingRemark(false);
      return;
    }
    if (trimmed.length > 64) {
      message.warning('备注不能超过64个字符');
      return;
    }
    setSavingRemark(true);
    try {
      await friendApi.setRemark(id, { remark: trimmed });
      message.success('备注已更新');
      queryClient.invalidateQueries({ queryKey: ['friends'] });
      setEditingRemark(false);
    } catch {
      message.error('更新备注失败');
    } finally {
      setSavingRemark(false);
    }
  };

  const startEditingRemark = () => {
    setRemarkValue(friendInfo?.remark || '');
    setEditingRemark(true);
  };

  // Subscribe to presence
  useEffect(() => {
    if (!id || wsStatus !== 'connected') return;
    wsSend({ type: 'presence.subscribe', user_ids: [id] });
  }, [id, wsStatus]);

  // Listen for presence
  useEffect(() => {
    const unsub = wsOn('presence.state', (payload: any) => {
      if (payload.user_id === id) {
        setOnline(payload.online === true);
      }
    });
    return unsub;
  }, [id]);

  const { data: user, isLoading } = useQuery({
    queryKey: ['user', id],
    queryFn: async () => {
      const res = await client.get(`/users/${id}`);
      return res.data.data;
    },
    enabled: !!id,
  });

  const { data: friendsList } = useQuery({
    queryKey: ['friends'],
    queryFn: () => friendApi.list(),
    enabled: !!id,
  });

  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: ['friend-groups'],
    queryFn: () => friendApi.listGroups(),
    enabled: !!id,
  });

  const groups = groupsData ?? [];
  const friendInfo = friendsList?.list?.find((f: FriendInfo) => f.user_id === id);
  const currentGroupId = friendInfo?.group_id ?? undefined;

  if (isLoading) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%' }}>
        <Spin />
      </div>
    );
  }

  if (!user) {
    return (
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', gap: 12 }}>
        <p style={{ color: 'var(--aim-text-tertiary)' }}>用户不存在</p>
        <Button onClick={() => navigate('/contacts')}>返回</Button>
      </div>
    );
  }

  return (
    <div style={{ padding: 32, width: '100%' }}>
      <Button onClick={() => navigate('/contacts')} style={{ marginBottom: 24 }}>← 返回</Button>

      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 32 }}>
        <Avatar name={user.username} src={user.avatar} size={64} />
        <div>
          <h2 style={{ color: 'var(--aim-text)', margin: 0, fontSize: 22 }}>
            {user.username}
            <span style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 6,
              marginLeft: 10,
              padding: '2px 10px',
              borderRadius: 10,
              fontSize: 12,
              fontWeight: 400,
              verticalAlign: 'middle',
              background: online ? 'rgba(16,185,129,0.1)' : 'rgba(156,163,175,0.1)',
              color: online ? 'var(--aim-success)' : 'var(--aim-text-tertiary)',
              border: `1px solid ${online ? 'rgba(16,185,129,0.3)' : 'rgba(156,163,175,0.2)'}`,
            }}>
              <PresenceDot online={online} size="small" />
              {online ? '在线' : '离线'}
            </span>
          </h2>
          <p style={{ color: 'var(--aim-text-secondary)', margin: '4px 0 0' }}>
            {user.gender === 1 ? '男' : user.gender === 2 ? '女' : ''}
          </p>
        </div>
      </div>

      <Descriptions {...descProps}>
        <Descriptions.Item label="用户名">{user.username || '-'}</Descriptions.Item>
        <Descriptions.Item label="手机">{user.phone || '-'}</Descriptions.Item>
        <Descriptions.Item label="邮箱">{user.email || '-'}</Descriptions.Item>
        <Descriptions.Item label="个人简介">{user.bio || '-'}</Descriptions.Item>
      </Descriptions>

      <div style={{ marginTop: 16 }}>
        <Descriptions {...descProps}>
          <Descriptions.Item label="备注">
            {editingRemark ? (
              <Input
                ref={remarkInputRef}
                value={remarkValue}
                onChange={(e) => setRemarkValue(e.target.value)}
                onBlur={handleRemarkSave}
                onPressEnter={handleRemarkSave}
                onKeyDown={(e) => { if (e.key === 'Escape') { setEditingRemark(false); } }}
                disabled={savingRemark}
                maxLength={64}
                size="small"
                style={{ width: 200 }}
              />
            ) : (
              <span
                onClick={startEditingRemark}
                style={{ cursor: 'pointer', color: friendInfo?.remark ? undefined : 'var(--aim-text-tertiary)' }}
              >
                {friendInfo?.remark || '点击设置备注'}
              </span>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="分组">
            <Select
              value={currentGroupId}
              allowClear
              placeholder="默认分组"
              style={{ width: 160 }}
              loading={groupsLoading}
              disabled={groupsLoading}
              onChange={async (newGroupId) => {
                if (!id) return;
                try {
                  await friendApi.setGroup(id, newGroupId == null ? { clear_group_id: true } : { group_id: newGroupId });
                  message.success('分组已更新');
                  queryClient.invalidateQueries({ queryKey: ['friends'] });
                } catch {
                  message.error('更新分组失败');
                }
              }}
              options={[
                ...groups.map((g: FriendGroup) => ({ value: g.id, label: g.name })),
              ]}
            />
          </Descriptions.Item>
        </Descriptions>
      </div>

      <Button
        type="primary"
        size="large"
        style={{ marginTop: 24, width: '100%' }}
        loading={sending}
        onClick={async () => {
          if (!id) return;
          setSending(true);
          try {
            const conv = await convApi.create({ type: 'single', peer_user_id: id });
            navigate(`/conversations/${conv.id}`);
          } catch {
            message.error('创建会话失败');
          } finally {
            setSending(false);
          }
        }}
      >
        发消息
      </Button>
      {friendInfo && (
        <Button danger style={{ marginTop: 12 }} onClick={() => {
          if (!id) return;
          Modal.confirm({
            title: '删除好友',
            content: `删除 ${friendInfo.remark || friendInfo.username}？`,
            okText: '删除',
            okButtonProps: { danger: true },
            cancelText: '取消',
            onOk: async () => {
              await friendApi.deleteFriend(id);
              await queryClient.invalidateQueries({ queryKey: ['friends'] });
              await queryClient.invalidateQueries({ queryKey: ['friend-groups'] });
              navigate('/contacts', { replace: true });
            },
          });
        }}>删除好友</Button>
      )}
    </div>
  );
}
