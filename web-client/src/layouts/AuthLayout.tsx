import { Outlet, Navigate } from 'react-router-dom';
import { useAuthStore } from '@/stores/auth';

import './AuthLayout.css';

export function AuthLayout() {
  const isAuth = useAuthStore((s) => s.isAuthenticated);
  if (isAuth) return <Navigate to="/conversations" replace />;

  return (
    <div className="auth-layout">
      <div className="auth-bg" />
      <div className="auth-container">
        <div className="auth-brand">
          <div className="auth-logo">A</div>
          <h1 className="auth-title">AIM</h1>
          <p className="auth-subtitle">AI 即时消息平台</p>
        </div>
        <div className="auth-card">
          <Outlet />
        </div>
        <div className="auth-footer">
          <span>AIM v2</span>
        </div>
      </div>
    </div>
  );
}
