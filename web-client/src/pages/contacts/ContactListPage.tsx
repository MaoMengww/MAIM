import { useState, useEffect } from 'react';
import { Outlet, useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { UserAddOutlined, UnorderedListOutlined, InboxOutlined, PlusOutlined, EllipsisOutlined } from '@ant-design/icons';
import { Modal, Input, message, List, Button, Tag, Drawer, Select, Dropdown } from 'antd';
import { friendApi } from '@/services/friend';
import client from '@/services/client';
import type { FriendInfo, FriendGroup } from '@/types/model';
import type { MenuProps } from 'antd';
import { Avatar } from '@/components/common/Avatar';
import { PresenceDot } from '@/components/common/PresenceDot';
import { wsSend, wsOn } from '@/services/ws';
import { useWSStore } from '@/stores/ws';

import './ContactListPage.css';

export function ContactListPage() {
  const navigate = useNavigate();
  const { id: activeId } = useParams();
  const [addOpen, setAddOpen] = useState(false);
  const [reqOpen, setReqOpen] = useState(false);
  const [searchKeyword, setSearchKeyword] = useState('');
  const [searchResults, setSearchResults] = useState<any[]>([]);
  const [searching, setSearching] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [newGroupName, setNewGroupName] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameGroupId, setRenameGroupId] = useState<number | null>(null);
  const [renameValue, setRenameValue] = useState('');

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['friends'],
    queryFn: () => friendApi.list(),
  });

  const { data: groupsData, refetch: refetchGroups } = useQuery({
    queryKey: ['friend-groups'],
    queryFn: () => friendApi.listGroups(),
  });
  const groups = groupsData ?? [];

  const { data: pendingReqs, isLoading: reqsLoading } = useQuery({
    queryKey: ['friend-requests'],
    queryFn: () => friendApi.pendingRequests(),
    enabled: reqOpen,
  });

  const friends = data?.list ?? [];

  // Group friends by group_id, preserving server group order
  const friendsByGroup = friends.reduce((acc, f) => {
    const gid = f.group_id || 0;
    if (!acc[gid]) acc[gid] = [];
    acc[gid].push(f);
    return acc;
  }, {} as Record<number, FriendInfo[]>);

  // Ordered group IDs: server groups first, then default group (id=0) if it has members
  const groupOrder: number[] = groups.map((g: FriendGroup) => g.id);
  if (friends.some((f: FriendInfo) => !f.group_id)) {
    groupOrder.push(0);
  }

  const [collapsedGroups, setCollapsedGroups] = useState<Set<number>>(new Set());

  const toggleGroup = (gid: number) => {
    setCollapsedGroups(prev => {
      const next = new Set(prev);
      if (next.has(gid)) next.delete(gid);
      else next.add(gid);
      return next;
    });
  };

  const wsStatus = useWSStore((s) => s.status);
  const [onlineStatus, setOnlineStatus] = useState<Record<string, boolean>>({});

  // 好友列表加载后订阅在线状态
  useEffect(() => {
    const ids = friends.map((f: any) => String(f.user_id));
    if (ids.length === 0 || wsStatus !== 'connected') return;
    wsSend({ type: 'subscribe_presence', user_ids: ids });
  }, [data, wsStatus]);

  // 监听 WebSocket presence 事件
  useEffect(() => {
    const unsub = wsOn('presence', (payload: any) => {
      setOnlineStatus((prev) => ({ ...prev, [String(payload.user_id)]: payload.status === 'online' }));
    });
    return unsub;
  }, []);

  const handleSearch = async () => {
    if (!searchKeyword.trim()) return;
    setSearching(true);
    try {
      const res = await client.post('/users/search', { keyword: searchKeyword });
      const users = res.data.data?.users ?? [];
      setSearchResults(users);
    } catch {
      message.error('搜索失败');
    } finally {
      setSearching(false);
    }
  };

  const handleSendRequest = async (userId: number) => {
    try {
      await friendApi.sendRequest({ to_user_id: userId, message: '你好，加个好友' });
      message.success('好友请求已发送');
    } catch {
      message.error('发送失败');
    }
  };

  const handleAccept = async (id: number) => {
    try {
      await friendApi.acceptRequest(id);
      message.success('已同意');
      refetch();
    } catch {
      message.error('操作失败');
    }
  };

  const handleReject = async (id: number) => {
    try {
      await friendApi.rejectRequest(id);
      message.success('已拒绝');
      refetch();
    } catch {
      message.error('操作失败');
    }
  };

  const handleCreateGroup = async () => {
    if (!newGroupName.trim()) {
      message.warning('请输入分组名称');
      return;
    }
    try {
      await friendApi.createGroup({ name: newGroupName.trim() });
      message.success('已创建');
      setCreateOpen(false);
      setNewGroupName('');
      refetch();
      refetchGroups();
    } catch {
      message.error('创建失败');
    }
  };

  return (
    <div className="contact-page">
      <div className="contact-panel">
        <div className="contact-header">
          <h2>联系人</h2>
        </div>

        <div className="contact-search">
          <input className="contact-search-input" placeholder="搜索联系人..." />
        </div>

        <div className="contact-actions">
          <button className="contact-action-btn" onClick={() => setAddOpen(true)}>
            <UserAddOutlined /> 添加好友
          </button>
          <button className="contact-action-btn" onClick={() => setReqOpen(true)}>
            <InboxOutlined /> 好友请求
            {pendingReqs?.length ? <Tag style={{ marginLeft: 4 }}>{pendingReqs.length}</Tag> : null}
          </button>
          <button className="contact-action-btn" onClick={() => setDrawerOpen(true)}><UnorderedListOutlined /> 管理分组</button>
        </div>

        <div className="contact-list">
          {isLoading && <div className="contact-empty">加载中...</div>}

          {!isLoading && friends.length === 0 && (
            <div className="contact-empty">
              <p>暂无联系人</p>
              <p style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', marginTop: 4 }}>搜索用户或添加好友开始聊天</p>
            </div>
          )}

          {!isLoading && friends.length > 0 && groupOrder.map((gid: number) => {
            const group = groups.find((g: FriendGroup) => g.id === gid);
            const name = group?.name || '默认分组';
            const members = friendsByGroup[gid] || [];
            const collapsed = collapsedGroups.has(gid);

            return (
              <div key={gid}>
                <div className="contact-group-header" onClick={() => toggleGroup(gid)}>
                  <span className={`contact-group-arrow ${collapsed ? 'collapsed' : ''}`}>&#x25BC;</span>
                  <span className="contact-group-name">{name}</span>
                  <span className="contact-group-count">{members.length}</span>
                </div>
                {!collapsed && members.map((f: FriendInfo) => (
                  <div key={f.user_id} className="contact-item" onClick={() => navigate(`/contacts/${f.user_id}`)}>
                    <div style={{ position: 'relative', display: 'inline-block' }}>
                      <Avatar name={f.remark || f.username} src={f.avatar} size={40} />
                      <div style={{ position: 'absolute', bottom: 0, right: 0 }}>
                        <PresenceDot online={!!onlineStatus[f.user_id]} size="small" />
                      </div>
                    </div>
                    <div className="contact-item-info">
                      <div className="contact-item-name">{f.remark || f.username}</div>
                      <span style={{ fontSize: 11, color: onlineStatus[f.user_id] ? 'var(--aim-success)' : 'var(--aim-text-tertiary)' }}>
                        {onlineStatus[f.user_id] ? '在线' : '离线'}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </div>

      {/* Add Friend Modal */}
      <Modal
        title="添加好友"
        open={addOpen}
        onCancel={() => { setAddOpen(false); setSearchResults([]); setSearchKeyword(''); }}
        footer={null}
      >
        <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
          <Input
            placeholder="搜索用户名"
            value={searchKeyword}
            onChange={(e) => setSearchKeyword(e.target.value)}
            onPressEnter={handleSearch}
          />
          <Button onClick={handleSearch} loading={searching}>搜索</Button>
        </div>
        <List
          dataSource={searchResults}
          locale={{ emptyText: searchKeyword ? '未找到用户' : '输入用户名搜索' }}
          renderItem={(user: any) => (
            <List.Item
              actions={[
                <Button type="primary" size="small" onClick={() => handleSendRequest(user.user_id || user.id)}>
                  添加
                </Button>,
              ]}
            >
              <List.Item.Meta
                avatar={<Avatar name={user.username} src={user.avatar} size={36} />}
                title={user.username}
                description={user.bio || ''}
              />
            </List.Item>
          )}
        />
      </Modal>

      {/* Friend Requests Modal */}
      <Modal
        title="好友请求"
        open={reqOpen}
        onCancel={() => setReqOpen(false)}
        footer={null}
      >
        <List
          dataSource={pendingReqs ?? []}
          loading={reqsLoading}
          locale={{ emptyText: '暂无好友请求' }}
          renderItem={(req: any) => (
            <List.Item
              actions={[
                <Button type="primary" size="small" onClick={() => handleAccept(req.request_id)}>
                  同意
                </Button>,
                <Button size="small" onClick={() => handleReject(req.request_id)}>
                  拒绝
                </Button>,
              ]}
            >
              <List.Item.Meta
                avatar={<Avatar name={req.from_username || `用户${req.from_user_id}`} src={req.from_avatar} size={36} />}
                title={
                  <span
                    style={{ cursor: 'pointer', color: 'var(--aim-text-link)' }}
                    onClick={() => navigate(`/contacts/${req.from_user_id}`)}
                  >
                    {req.from_username || `用户 ${req.from_user_id}`}
                  </span>
                }
                description={req.message || '请求添加好友'}
              />
            </List.Item>
          )}
        />
      </Modal>

      <Drawer
        title="管理分组"
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        placement="right"
        width={360}
      >
        <Button block icon={<PlusOutlined />} onClick={() => setCreateOpen(true)} style={{ marginBottom: 16 }}>
          新建分组
        </Button>

        {/* Create Group Modal */}
        <Modal
          title="新建分组"
          open={createOpen}
          onCancel={() => { setCreateOpen(false); setNewGroupName(''); }}
          onOk={handleCreateGroup}
        >
          <Input
            placeholder="分组名称"
            value={newGroupName}
            onChange={(e) => setNewGroupName(e.target.value)}
            onPressEnter={handleCreateGroup}
            maxLength={16}
          />
        </Modal>

        {/* Rename Group Modal */}
        <Modal
          title="重命名分组"
          open={renameOpen}
          onCancel={() => { setRenameOpen(false); setRenameGroupId(null); }}
          onOk={async () => {
            if (!renameValue.trim() || !renameGroupId) return;
            try {
              await friendApi.renameGroup(renameGroupId, renameValue.trim());
              message.success('已重命名');
              setRenameOpen(false);
              setRenameGroupId(null);
              refetch();
              refetchGroups();
            } catch {
              message.error('重命名失败');
            }
          }}
        >
          <Input
            placeholder="分组名称"
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            maxLength={16}
          />
        </Modal>

        <div className="contact-group-manage-list">
          {groupOrder.map((gid: number) => {
            const group = groups.find((g: FriendGroup) => g.id === gid);
            const name = group?.name || '默认分组';
            const members: FriendInfo[] = friendsByGroup[gid] || [];

            return (
              <div key={gid} className="group-manage-section">
                <div className="group-manage-header">
                  <span className="group-manage-name">{name}</span>
                  <span className="group-manage-count">{members.length}人</span>
                  {gid !== 0 && (
                    <Dropdown menu={{
                      items: [
                        {
                          key: 'rename',
                          label: '重命名',
                          onClick: () => { setRenameGroupId(gid); setRenameValue(name); setRenameOpen(true); },
                        },
                        {
                          key: 'delete',
                          label: '删除',
                          danger: true,
                          onClick: () => {
                            Modal.confirm({
                              title: `删除分组「${name}」`,
                              content: '组内好友将移入默认分组',
                              onOk: async () => {
                                try {
                                  await friendApi.deleteGroup(gid);
                                  message.success('已删除');
                                  refetch();
                                  refetchGroups();
                                } catch {
                                  message.error('删除失败');
                                }
                              },
                            });
                          },
                        },
                      ] as MenuProps['items'],
                    }} trigger={['click']}>
                      <Button type="text" size="small" icon={<EllipsisOutlined />} />
                    </Dropdown>
                  )}
                </div>
                {members.map((f: FriendInfo) => (
                  <div key={f.user_id} className="group-manage-item">
                    <Avatar name={f.remark || f.username} src={f.avatar} size={28} />
                    <span className="group-manage-item-name">{f.remark || f.username}</span>
                    <Select
                      size="small"
                      value={f.group_id || 0}
                      style={{ width: 100 }}
                      onChange={async (newGroupId) => {
                        try {
                          await friendApi.setGroup(f.user_id, { group_id: newGroupId });
                          message.success('已移动');
                          refetch();
                        } catch {
                          message.error('移动失败');
                        }
                      }}
                      options={[
                        { value: 0, label: '默认分组' },
                        ...groups.map((g: FriendGroup) => ({ value: g.id, label: g.name })),
                      ]}
                    />
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </Drawer>

      <div className="contact-main-panel">
        <Outlet />
      </div>
    </div>
  );
}
