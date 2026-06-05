import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { useAuthStore } from '@/stores/auth';
import { authApi } from '@/services/auth';

import './AuthPage.css';

export function LoginPage() {
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const [account, setAccount] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!account || !password) {
      setError('请填写账号和密码');
      return;
    }
    setLoading(true);
    setError('');
    try {
      const resp = await authApi.login({ account, password });
      setAuth(resp.tokens.access_token, resp.tokens.refresh_token, resp.user);
      navigate('/conversations', { replace: true });
    } catch (err: any) {
      setError(err?.response?.data?.message || '登录失败，请重试');
    } finally {
      setLoading(false);
    }
  };

  return (
    <form className="auth-form" onSubmit={handleSubmit}>
      <h2 className="auth-form-title">登录</h2>

      {error && <div className="auth-error">{error}</div>}

      <div className="auth-field">
        <label>账号</label>
        <input
          className="auth-input"
          type="text"
          placeholder="用户名 / 手机号 / 邮箱"
          value={account}
          onChange={(e) => setAccount(e.target.value)}
          autoFocus
        />
      </div>

      <div className="auth-field">
        <label>密码</label>
        <input
          className="auth-input"
          type="password"
          placeholder="请输入密码"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </div>

      <button className="auth-submit" type="submit" disabled={loading}>
        {loading ? '登录中...' : '登录'}
      </button>

      <p className="auth-switch">
        还没有账号？<Link to="/register">注册</Link>
      </p>
    </form>
  );
}
