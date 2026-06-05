import { useEffect, useState } from 'react';
import { Outlet, useNavigate, useLocation, Navigate } from 'react-router-dom';
import {
  MessageOutlined,
  TeamOutlined,
  RobotOutlined,
  BookOutlined,
  SettingOutlined,
  PoweroffOutlined,
  BellOutlined,
  InfoCircleOutlined,
  WarningOutlined,
  CheckCircleOutlined,
  NotificationOutlined,
} from '@ant-design/icons';
import { Badge, Popover, List, Button, Empty, Typography } from 'antd';
import { useAuthStore } from '@/stores/auth';
import { useUIStore } from '@/stores/ui';
import { wsConnect, wsDisconnect } from '@/services/ws';
import { useNotifications } from '@/hooks/useNotifications';
import { Avatar } from '@/components/common/Avatar';

import './MainLayout.css';

const NAV_ITEMS = [
  { key: 'conversations', icon: MessageOutlined, label: '消息' },
  { key: 'contacts', icon: TeamOutlined, label: '联系人' },
  { key: 'bots', icon: RobotOutlined, label: 'Bot' },
  { key: 'knowledge', icon: BookOutlined, label: '知识库' },
  { key: 'settings', icon: SettingOutlined, label: '设置' },
];

export function MainLayout() {
  const { isAuthenticated, user, logout } = useAuthStore();
  const sidebarKey = useUIStore((s) => s.sidebarKey);
  const setSidebarKey = useUIStore((s) => s.setSidebarKey);
  const navigate = useNavigate();
  const location = useLocation();
  const [notifOpen, setNotifOpen] = useState(false);
  const { listQuery, markReadMutation, markAllReadMutation } = useNotifications();
  const notifications = listQuery.data?.list ?? [];
  const unreadCount = notifications.filter((n: any) => !n.is_read).length;

  useEffect(() => {
    if (isAuthenticated) {
      wsConnect();
    }
    return () => wsDisconnect();
  }, [isAuthenticated]);

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />;
  }

  const currentKey = NAV_ITEMS.find((item) =>
    location.pathname.startsWith(`/${item.key}`),
  )?.key ?? sidebarKey;

  const handleNav = (key: string) => {
    setSidebarKey(key);
    navigate(`/${key}`);
  };

  return (
    <div className="main-layout">
      <nav className="sidebar">
        <div className="sidebar-brand">
          <button className="sidebar-logo" onClick={() => navigate('/conversations')} title="AIM">
            A
          </button>
        </div>

        <div className="sidebar-nav">
          {NAV_ITEMS.map((item) => {
            const Icon = item.icon;
            return (
              <button
                key={item.key}
                className={`sidebar-nav-item ${currentKey === item.key ? 'active' : ''}`}
                onClick={() => handleNav(item.key)}
                title={item.label}
              >
                <Icon className="sidebar-nav-icon" />
                <span className="sidebar-nav-tooltip">{item.label}</span>
              </button>
            );
          })}
        </div>

        <Popover
          open={notifOpen}
          onOpenChange={setNotifOpen}
          trigger="click"
          placement="rightBottom"
          content={
            <div style={{ width: 340, maxHeight: 420, overflowY: 'auto' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8, padding: '0 4px' }}>
                <span style={{ fontWeight: 600, fontSize: 14, color: 'var(--aim-text)' }}>
                  通知 {unreadCount > 0 && <span style={{ color: 'var(--aim-primary, #1677ff)' }}>({unreadCount})</span>}
                </span>
                {unreadCount > 0 && (
                  <Button type="link" size="small" onClick={() => markAllReadMutation.mutate()}>
                    全部已读
                  </Button>
                )}
              </div>
              {notifications.length === 0 ? (
                <Empty description="暂无通知" image={Empty.PRESENTED_IMAGE_SIMPLE} />
              ) : (
                <List
                  dataSource={notifications.slice(0, 20)}
                  renderItem={(n: any) => {
                    const Icon = n.type === 2 ? WarningOutlined : n.type === 3 ? CheckCircleOutlined : n.type === 4 ? NotificationOutlined : InfoCircleOutlined;
                    const color = n.type === 2 ? 'var(--aim-warning, #faad14)' : n.type === 3 ? 'var(--aim-success, #52c41a)' : 'var(--aim-primary, #1677ff)';
                    const content = typeof n.content === 'string' ? n.content : n.content?.text || n.content?.action || '';
                    return (
                      <List.Item
                        style={{
                          cursor: 'pointer',
                          background: n.is_read ? 'transparent' : 'rgba(22,119,255,0.04)',
                          borderRadius: 8,
                          marginBottom: 4,
                          padding: '8px 10px',
                          border: n.is_read ? '1px solid transparent' : '1px solid rgba(22,119,255,0.12)',
                          transition: 'all 0.2s',
                        }}
                        onClick={() => { markReadMutation.mutate(n.id); }}
                      >
                        <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start', width: '100%' }}>
                          <div style={{
                            width: 32, height: 32, borderRadius: 8,
                            background: `${color}15`, display: 'flex',
                            alignItems: 'center', justifyContent: 'center',
                            flexShrink: 0, marginTop: 2,
                          }}>
                            <Icon style={{ fontSize: 14, color }} />
                          </div>
                          <div style={{ flex: 1, minWidth: 0 }}>
                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
                              <Typography.Text strong style={{ fontSize: 13, color: 'var(--aim-text)' }} ellipsis>
                                {n.title}
                              </Typography.Text>
                              {!n.is_read && <span style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--aim-primary, #1677ff)', flexShrink: 0 }} />}
                            </div>
                            {content && (
                              <Typography.Text type="secondary" style={{ fontSize: 12 }} ellipsis>
                                {content}
                              </Typography.Text>
                            )}
                            <div style={{ fontSize: 11, color: 'var(--aim-text-tertiary)', marginTop: 2 }}>
                              {n.created_at ? new Date(n.created_at * 1000).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''}
                            </div>
                          </div>
                        </div>
                      </List.Item>
                    );
                  }}
                />
              )}
            </div>
          }
        >
          <div className="sidebar-notif-btn">
            <Badge count={unreadCount} size="small" offset={[2, -2]}>
              <BellOutlined />
            </Badge>
          </div>
        </Popover>

        <div className="sidebar-footer">
          <div className="sidebar-user" onClick={() => navigate('/settings')} title={user?.username}>
            <Avatar name={user?.username} src={user?.avatar} size={32} />
          </div>
          <button className="sidebar-logout" onClick={() => { wsDisconnect(); logout(); }} title="退出登录">
            <PoweroffOutlined />
          </button>
        </div>
      </nav>

      <main className="main-content">
        <Outlet />
      </main>
    </div>
  );
}
