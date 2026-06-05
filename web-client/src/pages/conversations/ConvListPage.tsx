import { useState, useEffect, useRef } from 'react';
import { Outlet, useNavigate, useParams } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Modal, Input, Select, message, Dropdown } from 'antd';
import type { MenuProps } from 'antd';
import { MoreOutlined, PushpinOutlined, BellOutlined, SettingOutlined, TeamOutlined, DeleteOutlined, LogoutOutlined, SearchOutlined } from '@ant-design/icons';
import { convApi } from '@/services/conversation';
import { msgApi } from '@/services/message';
import { friendApi } from '@/services/friend';
import { Avatar } from '@/components/common/Avatar';
import { PresenceDot } from '@/components/common/PresenceDot';
import { useAuthStore } from '@/stores/auth';
import { wsOn, wsSend } from '@/services/ws';
import { useWSStore } from '@/stores/ws';
import type { Conversation, Message } from '@/types/model';
import type { SearchMessagesReq } from '@/types/api';

import './ConvListPage.css';

function formatTime(ts: number): string {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  const msgDate = new Date(d.getFullYear(), d.getMonth(), d.getDate());

  if (msgDate.getTime() === today.getTime()) {
    // Today: HH:mm
    return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
  }
  if (msgDate.getTime() === yesterday.getTime()) {
    // Yesterday: 昨天 HH:mm
    return `昨天 ${d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}`;
  }
  if (d.getFullYear() === now.getFullYear()) {
    // This year: MM/DD
    return `${d.getMonth() + 1}/${d.getDate()}`;
  }
  // Previous years: YYYY/MM/DD
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`;
}

const MSG_TYPE_LABELS: Record<number, string> = {
  1: '文本', 2: '图片', 3: '文件', 4: '视频',
  5: '语音', 6: '位置', 7: '系统', 8: '自定义', 9: 'Bot',
};

function convTitle(c: Conversation): string {
  return c.name || (c.type === 'group' ? '群聊' : '私聊');
}

function truncate(s: string, max = 20): string {
  if (!s || s.length <= max) return s;
  return s.slice(0, max) + '...';
}

function extractTextPreview(content: any): string {
  if (!content) return '';
  if (typeof content === 'string') return content;
  if (content.text) return typeof content.text === 'string' ? content.text : JSON.stringify(content.text);
  if (content.image) return '[图片]';
  if (content.file) return `[文件] ${content.file.name || content.file.file_name || ''}`;
  if (content.audio) return '[语音]';
  if (content.video) return '[视频]';
  if (content.file_name) return `[文件] ${content.file_name}`;
  if (content.image_url) return '[图片]';
  return '';
}

export function ConvListPage() {
  const navigate = useNavigate();
  const { id: activeId } = useParams();
  const queryClient = useQueryClient();
  const currentUserId = useAuthStore((s) => s.user?.id);
  const [groupOpen, setGroupOpen] = useState(false);
  const [groupName, setGroupName] = useState('');
  const [selectedMembers, setSelectedMembers] = useState<number[]>([]);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameConv, setRenameConv] = useState<Conversation | null>(null);
  const [renameName, setRenameName] = useState('');

  // Search state
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState<any[]>([]);
  const [searchTotal, setSearchTotal] = useState(0);
  const [isSearching, setIsSearching] = useState(false);
  const [searchHighlights, setSearchHighlights] = useState<Record<string, string>>({});
  const [typeCounts, setTypeCounts] = useState<{ msg_type: number; count: number }[]>([]);
  const [activeTypeFilters, setActiveTypeFilters] = useState<number[]>([]);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>();
  const [onlineStatus, setOnlineStatus] = useState<Record<string, boolean>>({});
  const [previewMap, setPreviewMap] = useState<Record<number, string>>({});

  const { data, isLoading } = useQuery({
    queryKey: ['conversations'],
    queryFn: () => convApi.list(),
  });

  const conversations = data?.list ?? [];

  // Real-time conv list updates via WebSocket
  useEffect(() => {
    const unsubMsg = wsOn('message.new', (payload: any) => {
      const convId = payload.message?.conv_id ?? payload.conversation_id;
      if (!convId) return;
      // Clear cached enriched preview so it gets re-fetched with sender name
      setPreviewMap((prev) => {
        const key = Number(convId);
        if (!prev[key]) return prev;
        const next = { ...prev };
        delete next[key];
        return next;
      });
      queryClient.setQueryData(['conversations'], (old: any) => {
        if (!old?.list) return old;
        return {
          ...old,
          list: old.list.map((c: any) =>
            Number(c.id) === Number(convId)
              ? {
                  ...c,
                  unread_count: String(convId) === activeId
                    ? 0
                    : (payload.unread_count ?? c.unread_count),
                  last_message_preview: payload.preview ?? c.last_message_preview,
                  updated_at: Math.floor(Date.now() / 1000),
                }
              : c
          ),
        };
      });
    });

    const unsubRead = wsOn('unread_count', (payload: any) => {
      queryClient.setQueryData(['conversations'], (old: any) => {
        if (!old?.list) return old;
        return {
          ...old,
          list: old.list.map((c: any) =>
            Number(c.id) === Number(payload.conv_id)
              ? { ...c, unread_count: payload.unread_count ?? 0 }
              : c
          ),
        };
      });
    });

    return () => { unsubMsg(); unsubRead(); };
  }, [queryClient, activeId]);

  // Re-sync conversations on WS reconnect
  const wsReconnectVersion = useWSStore((s) => s.reconnectVersion);
  useEffect(() => {
    if (wsReconnectVersion === 0) return;
    queryClient.invalidateQueries({ queryKey: ['conversations'] });
  }, [wsReconnectVersion, queryClient]);

  // Subscribe to presence for private conversation peers
  const wsStatus = useWSStore((s) => s.status);
  useEffect(() => {
    if (!conversations.length || wsStatus !== 'connected') return;
    const ids = conversations
      .filter((c: any) => c.type === 'private' && c.peer_user_id)
      .map((c: any) => String(c.peer_user_id));
    if (ids.length === 0) return;
    wsSend({ type: 'subscribe_presence', user_ids: ids });
  }, [conversations, wsStatus]);

  // Listen for presence events
  useEffect(() => {
    const unsub = wsOn('presence', (payload: any) => {
      setOnlineStatus((prev) => ({ ...prev, [String(payload.user_id)]: payload.status === 'online' }));
    });
    return unsub;
  }, []);

  const { data: friendsData } = useQuery({
    queryKey: ['friends'],
    queryFn: () => friendApi.list(),
  });

  const friends = friendsData?.list ?? [];
  // Build a map from conv_id to conversation for search results
  const convMap = new Map(conversations.map((c: Conversation) => [c.id, c]));

  // Enrich previews: fetch last message content for group chats (add sender name)
  useEffect(() => {
    if (!friendsData) return; // wait for friends list (needed for sender name lookup)

    const toFetch = conversations.filter(c => {
      if (!c.last_message_id || c.last_message_id <= 0) return false;
      if (previewMap[c.id]) return false; // already fetched
      return c.type === 'group' || !c.last_message_preview;
    }).slice(0, 30);

    if (!toFetch.length) return;

    (async () => {
      const results = await Promise.allSettled(
        toFetch.map(c => msgApi.getById(c.last_message_id))
      );

      const updates: Record<number, string> = {};
      toFetch.forEach((conv, i) => {
        if (results[i].status !== 'fulfilled') return;
        const msg = results[i].value;
        if (!msg) return;
        const text = extractTextPreview(msg?.content);
        if (!text) return;

        if (conv.type === 'group' && msg.from_user_id) {
          const sender = friends.find((f: any) => Number(f.user_id) === Number(msg.from_user_id));
          const name = sender?.remark || sender?.username || `用户${msg.from_user_id}`;
          updates[conv.id] = `${name}: ${text}`;
        } else {
          updates[conv.id] = text;
        }
      });

      if (Object.keys(updates).length) {
        setPreviewMap(prev => ({ ...prev, ...updates }));
      }
    })();
  }, [conversations, friends, friendsData, previewMap]);

  // Search effect with debounce
  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
    }
    const q = searchQuery.trim();
    if (!q) {
      setSearchResults([]);
      setSearchTotal(0);
      setSearchHighlights({});
      setTypeCounts([]);
      setIsSearching(false);
      return;
    }
    setIsSearching(true);
    debounceRef.current = setTimeout(async () => {
      try {
        const params: any = { keyword: q, page: 1, page_size: 20 };
        if (activeTypeFilters.length > 0) {
          params.message_types = activeTypeFilters;
        }
        const res = await msgApi.search(params);
        setSearchResults(res.list);
        setSearchTotal(res.total);
        setSearchHighlights(res.highlights ?? {});
        setTypeCounts(res.type_counts ?? []);
      } catch {
        setSearchResults([]);
        setSearchTotal(0);
        setSearchHighlights({});
        setTypeCounts([]);
      } finally {
        setIsSearching(false);
      }
    }, 300);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [searchQuery, activeTypeFilters]);

  const createGroup = useMutation({
    mutationFn: () =>
      convApi.create({
        type: 'group',
        group_name: groupName,
        member_ids: selectedMembers,
      }),
    onSuccess: () => {
      message.success('群聊创建成功');
      setGroupOpen(false);
      setGroupName('');
      setSelectedMembers([]);
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '创建失败'),
  });

  const renameMutation = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) =>
      convApi.update(id, { name }),
    onSuccess: () => {
      message.success('已重命名');
      setRenameOpen(false);
      setRenameConv(null);
      setRenameName('');
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '重命名失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => convApi.delete(id),
    onSuccess: (_data, id) => {
      message.success('已删除会话');
      queryClient.setQueryData(['conversations'], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((c: any) => c.id !== id) };
      });
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
      navigate('/conversations');
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '删除失败'),
  });

  const leaveMutation = useMutation({
    mutationFn: (convId: number) =>
      convApi.removeMembers(convId, [currentUserId!]),
    onSuccess: (_data, convId) => {
      message.success('已退出群聊');
      queryClient.setQueryData(['conversations'], (old: any) => {
        if (!old?.list) return old;
        return { ...old, list: old.list.filter((c: any) => c.id !== convId) };
      });
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
      navigate('/conversations');
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  const updateSettingMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: Record<string, unknown> }) =>
      convApi.updateSettings(id, data),
    onSuccess: (_data, variables) => {
      const action = variables.data.is_pinned !== undefined
        ? (variables.data.is_pinned ? '已置顶' : '已取消置顶')
        : (variables.data.is_muted ? '已开启免打扰' : '已关闭免打扰');
      message.success(action);
      queryClient.invalidateQueries({ queryKey: ['conversations'] });
    },
    onError: (err: any) => message.error(err?.response?.data?.message || '操作失败'),
  });

  function getConvMenuItems(conv: Conversation): MenuProps['items'] {
    const items: MenuProps['items'] = [
      {
        key: 'pin',
        icon: <PushpinOutlined />,
        label: conv.is_pinned ? '取消置顶' : '置顶会话',
      },
      {
        key: 'mute',
        icon: <BellOutlined />,
        label: conv.is_muted ? '取消免打扰' : '消息免打扰',
      },
    ];

    if (conv.type === 'group') {
      items.push(
        { type: 'divider' },
        {
          key: 'members',
          icon: <TeamOutlined />,
          label: '查看成员',
        },
        {
          key: 'settings',
          icon: <SettingOutlined />,
          label: '群聊设置',
        },
        { type: 'divider' },
        {
          key: 'leave',
          icon: <LogoutOutlined />,
          label: '退出群聊',
          danger: true,
        },
      );
    } else {
      items.push(
        { type: 'divider' },
        {
          key: 'delete',
          icon: <DeleteOutlined />,
          label: '删除会话',
          danger: true,
        },
      );
    }

    return items;
  }

  const handleMenuClick = (conv: Conversation, key: string) => {
    switch (key) {
      case 'pin':
        updateSettingMutation.mutate({ id: conv.id, data: { is_pinned: !conv.is_pinned } });
        break;
      case 'mute':
        updateSettingMutation.mutate({ id: conv.id, data: { is_muted: !conv.is_muted } });
        break;
      case 'members':
        navigate(`/conversations/${conv.id}`);
        break;
      case 'settings':
        setRenameConv(conv);
        setRenameName(conv.name || '');
        setRenameOpen(true);
        break;
      case 'leave':
        Modal.confirm({
          title: '退出群聊',
          content: `确定要退出「${convTitle(conv)}」吗？`,
          okText: '退出',
          okType: 'danger',
          cancelText: '取消',
          onOk: () => leaveMutation.mutate(conv.id),
        });
        break;
      case 'delete':
        Modal.confirm({
          title: '删除会话',
          content: '确定要删除此会话吗？',
          okText: '删除',
          okType: 'danger',
          cancelText: '取消',
          onOk: () => deleteMutation.mutate(conv.id),
        });
        break;
    }
  };

  return (
    <div className="conv-page">
      <div className="conv-sidebar-panel">
        <div className="conv-panel-header">
          <h2>消息</h2>
          <div style={{ display: 'flex', gap: 6 }}>
            <button className="conv-new-btn" onClick={() => setGroupOpen(true)} title="创建群聊" style={{ fontSize: 14 }}>👥</button>
            <button className="conv-new-btn" onClick={() => navigate('/contacts')} title="新建会话">+</button>
          </div>
        </div>

        <div className="conv-search-box">
          <SearchOutlined className="conv-search-icon" />
          <input
            className="conv-search-input"
            placeholder="搜索消息或会话..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
          {searchQuery && (
            <button className="conv-search-clear" onClick={() => setSearchQuery('')}>✕</button>
          )}
        </div>

        <div className="conv-list">
          {isSearching && <div className="conv-empty">搜索中...</div>}

          {!isSearching && searchQuery.trim() && searchResults.length === 0 && (
            <div className="conv-empty">
              <p>未找到相关消息</p>
            </div>
          )}

          {searchQuery.trim() && !isSearching && searchResults.length > 0 && (
            <>
              <div className="conv-search-header">搜索到 {searchTotal} 条消息</div>
              {typeCounts.length > 0 && (
                <div className="conv-type-chips">
                  <button
                    className={`conv-type-chip ${activeTypeFilters.length === 0 ? 'active' : ''}`}
                    onClick={() => setActiveTypeFilters([])}
                  >全部({searchTotal})</button>
                  {typeCounts.map((tc) => {
                    const selected = activeTypeFilters.includes(tc.msg_type);
                    return (
                      <button
                        key={tc.msg_type}
                        className={`conv-type-chip ${selected ? 'active' : ''}`}
                        onClick={() => {
                          setActiveTypeFilters((prev) =>
                            selected ? prev.filter((t) => t !== tc.msg_type) : [...prev, tc.msg_type]
                          );
                        }}
                      >{MSG_TYPE_LABELS[tc.msg_type] || `类型${tc.msg_type}`}({tc.count})</button>
                    );
                  })}
                </div>
              )}
              {searchResults.map((msg: any) => {
                const conv = convMap.get(msg.conversation_id);
                const highlight = searchHighlights[String(msg.message_id)];
                return (
                  <div
                    key={msg.message_id}
                    className="conv-search-result-item"
                    onClick={() => {
                      setSearchQuery('');
                      navigate(`/conversations/${msg.conversation_id}`);
                    }}
                  >
                    <div className="conv-search-result-top">
                      <span className="conv-search-result-name">
                        {conv ? convTitle(conv) : `会话 ${msg.conversation_id}`}
                      </span>
                      <span className="conv-search-result-time">
                        {formatTime(msg.created_at)}
                      </span>
                    </div>
                    <div className="conv-search-result-preview">
                      {highlight ? (
                        <span dangerouslySetInnerHTML={{ __html: highlight }} />
                      ) : extractTextPreview(msg.content)}
                    </div>
                  </div>
                );
              })}
            </>
          )}

          {!searchQuery.trim() && isLoading && conversations.length === 0 && <div className="conv-empty">加载中...</div>}
          {!searchQuery.trim() && !isLoading && conversations.length === 0 && (
            <div className="conv-empty">
              <p>暂无会话</p>
              <p style={{ fontSize: 12, color: 'var(--aim-text-tertiary)', marginTop: 4 }}>在联系人中发起新聊天</p>
            </div>
          )}

          {!searchQuery.trim() && conversations.map((conv) => (
            <div key={conv.id} className="conv-item-row">
              <div
                className={`conv-item ${String(activeId) === String(conv.id) ? 'active' : ''}`}
                onClick={() => navigate(`/conversations/${conv.id}`)}
              >
                <div className="conv-item-avatar">
                  <Avatar name={convTitle(conv)} src={conv.avatar} size={44} />
                  {conv.unread_count > 0 && (
                    <span className="conv-item-badge">
                      {conv.unread_count > 99 ? '99+' : conv.unread_count}
                    </span>
                  )}
                  {conv.type === 'private' && (conv as any).peer_user_id && (
                    <PresenceDot
                      online={!!onlineStatus[String((conv as any).peer_user_id)]}
                      size="small"
                      className={conv.is_muted ? 'conv-item-online-muted' : ''}
                    />
                  )}
                </div>
                <div className="conv-item-info">
                  <div className="conv-item-top">
                    <span className="conv-item-name">{convTitle(conv)}</span>
                    <span className="conv-item-time">
                      {formatTime(conv.last_message_id ? conv.updated_at : conv.created_at)}
                    </span>
                  </div>
                  <div className="conv-item-bottom">
                    <span className="conv-item-preview">
                      {truncate(previewMap[conv.id] ?? conv.last_message_preview) || (conv.type === 'group' ? `共${conv.member_count}人` : '')}
                    </span>
                  </div>
                </div>
              </div>
              <Dropdown
                menu={{ items: getConvMenuItems(conv), onClick: ({ key }) => handleMenuClick(conv, key) }}
                trigger={['click']}
              >
                <button className="conv-item-more-btn">
                  <MoreOutlined />
                </button>
              </Dropdown>
            </div>
          ))}
        </div>
      </div>

      <div className="conv-main-panel">
        <Outlet />
      </div>

      <Modal
        title="创建群聊"
        open={groupOpen}
        onOk={() => createGroup.mutate()}
        onCancel={() => { setGroupOpen(false); setGroupName(''); setSelectedMembers([]); }}
        confirmLoading={createGroup.isPending}
        okText="创建"
        cancelText="取消"
      >
        <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Input placeholder="群聊名称" value={groupName} onChange={(e) => setGroupName(e.target.value)} />
          <Select
            mode="multiple"
            placeholder="选择成员"
            style={{ width: '100%' }}
            value={selectedMembers}
            onChange={setSelectedMembers}
            options={friends.map((f: any) => ({
              value: f.user_id,
              label: f.remark || f.username,
            }))}
          />
        </div>
      </Modal>

      <Modal
        title="重命名群聊"
        open={renameOpen}
        onOk={() => {
          if (renameConv && renameName.trim()) {
            renameMutation.mutate({ id: renameConv.id, name: renameName.trim() });
          }
        }}
        onCancel={() => { setRenameOpen(false); setRenameConv(null); setRenameName(''); }}
        confirmLoading={renameMutation.isPending}
        okText="保存"
        cancelText="取消"
      >
        <div style={{ marginTop: 16 }}>
          <Input
            placeholder="群聊名称"
            value={renameName}
            onChange={(e) => setRenameName(e.target.value)}
            maxLength={64}
          />
        </div>
      </Modal>
    </div>
  );
}
