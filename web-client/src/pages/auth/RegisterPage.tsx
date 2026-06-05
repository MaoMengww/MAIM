import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { useAuthStore } from '@/stores/auth';
import { authApi } from '@/services/auth';

import './AuthPage.css';

export function RegisterPage() {
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const [form, setForm] = useState({ username: '', password: '' });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const update = (key: string, val: string) => setForm((f) => ({ ...f, [key]: val }));

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.username || !form.password) {
      setError('请填写用户名和密码');
      return;
    }
    setLoading(true);
    setError('');
    try {
      const resp = await authApi.register({
        username: form.username,
        password: form.password,
      });
      setAuth(resp.tokens.access_token, resp.tokens.refresh_token, resp.user);
      navigate('/conversations', { replace: true });
    } catch (err: any) {
      setError(err?.response?.data?.message || '注册失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <form className="auth-form" onSubmit={handleSubmit}>
      <h2 className="auth-form-title">注册</h2>

      {error && <div className="auth-error">{error}</div>}

      <div className="auth-field">
        <label>用户名 *</label>
        <input className="auth-input" type="text" placeholder="输入用户名" value={form.username} onChange={(e) => update('username', e.target.value)} autoFocus />
      </div>
      <div className="auth-field">
        <label>密码 *</label>
        <input className="auth-input" type="password" placeholder="至少6位" value={form.password} onChange={(e) => update('password', e.target.value)} />
      </div>

      <button className="auth-submit" type="submit" disabled={loading}>
        {loading ? '注册中...' : '注册'}
      </button>

      <p className="auth-switch">
        已有账号？<Link to="/login">登录</Link>
      </p>
    </form>
  );
}
