import { Outlet, useNavigate, useLocation } from 'react-router-dom';

import './SettingsPage.css';

export { ProfilePage } from './ProfilePage';

export function SettingsPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const currentSection = location.pathname === '/settings' ? 'settings' : (location.pathname.split('/').pop() || 'settings');

  return (
    <div className="settings-page">
      <div className="settings-sidebar">
        <div className="settings-menu">
          <h3>设置</h3>
          <button
            className={`settings-menu-item ${currentSection === 'settings' ? 'active' : ''}`}
            onClick={() => navigate('/settings')}
          >
            个人资料
          </button>
          <button
            className={`settings-menu-item ${currentSection === 'models' ? 'active' : ''}`}
            onClick={() => navigate('/settings/models')}
          >
            模型管理
          </button>
          <button
            className={`settings-menu-item ${currentSection === 'mcp' ? 'active' : ''}`}
            onClick={() => navigate('/settings/mcp')}
          >
            MCP 服务器
          </button>
          <button
            className={`settings-menu-item ${currentSection === 'billing' ? 'active' : ''}`}
            onClick={() => navigate('/settings/billing')}
          >
            账单管理
          </button>
        </div>
      </div>
      <div className="settings-content">
        <Outlet />
      </div>
    </div>
  );
}
