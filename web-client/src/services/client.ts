import axios, { type InternalAxiosRequestConfig } from 'axios';
import { useAuthStore } from '@/stores/auth';
import type { APIResponse, LoginResp } from '@/types/model';

type SessionRequest = InternalAxiosRequestConfig & { _authRevision?: number; _retry?: boolean };

const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8080';

const client = axios.create({
  baseURL: `${API_BASE}/api/v1`,
  timeout: 15000,
  transformResponse: [(data: unknown) => typeof data === 'string' ? JSON.parse(data) : data],
});

// ─── Request: inject JWT ───
client.interceptors.request.use((config: SessionRequest) => {
  const auth = useAuthStore.getState();
  if (config._authRevision !== undefined && config._authRevision !== auth.revision) {
    throw new axios.CanceledError('账号已切换');
  }
  config._authRevision = auth.revision;
  if (auth.token) config.headers.Authorization = `Bearer ${auth.token}`;
  else delete config.headers.Authorization;
  return config;
});

// ─── Business error code → Chinese message fallback ───
// Backend now returns biz codes (1000-1018) instead of HTTP codes in JSON body.
// See pkg/errors/errors.go for the authoritative list.
const errorMessages: Record<number, string> = {
  1000: '未知错误',
  1001: '请求参数错误',
  1002: '请先登录',
  1003: '权限不足',
  1004: '资源不存在',
  1005: '资源冲突',
  1006: '服务器内部错误',
  1007: '请求超时',
  1008: '请求过于频繁，请稍后再试',
  1009: '服务暂不可用',
  1010: '数据库错误',
  1011: '缓存错误',
  1012: '消息队列错误',
  1013: 'RPC 调用错误',
  1014: 'IO 错误',
  1015: 'Bot 不存在',
  1016: 'Bot 操作受限',
  1017: 'Webhook 签名无效',
  1018: 'Webhook 时间戳过期',
};

function mapErrorMessage(code: number, originalMessage: string): string {
  // Prefer the backend's message; use Chinese fallback only when message is generic or empty
  const fallback = errorMessages[code];
  if (!fallback) return originalMessage;
  // If the original message already contains the fallback or is empty, use fallback
  if (!originalMessage || originalMessage === 'ok') return fallback;
  return originalMessage;
}

// ─── Response: unwrap & 401 refresh ───
let refreshJob: { revision: number; promise: Promise<string | null> } | null = null;

export function refreshSession(): Promise<string | null> {
  const { refreshToken, revision } = useAuthStore.getState();
  if (!refreshToken) return Promise.resolve(null);
  if (refreshJob?.revision === revision) return refreshJob.promise;
  const promise = (async () => {
    try {
      const resp = await axios.post<APIResponse<LoginResp>>(
        `${API_BASE}/api/v1/auth/refresh`, { refresh_token: refreshToken },
      );
      if (resp.data.code !== 0) throw new Error(resp.data.message);
      const { tokens, user } = resp.data.data;
      return useAuthStore.getState().applyRefresh(revision, refreshToken, tokens, user)
        ? tokens.access_token : null;
    } catch {
      const current = useAuthStore.getState();
      if (current.revision === revision && current.refreshToken === refreshToken) current.logout();
      return null;
    } finally {
      if (refreshJob?.revision === revision) refreshJob = null;
    }
  })();
  refreshJob = { revision, promise };
  return promise;
}

client.interceptors.response.use(
  (resp) => {
    if ((resp.config as SessionRequest)._authRevision !== useAuthStore.getState().revision) {
      return Promise.reject(new axios.CanceledError('账号已切换'));
    }
    // Backend guarantees code=0 for success. If we receive a non-zero code
    // on a 2xx response, treat it as a business error (belt and suspenders).
    if (resp.data && typeof resp.data === 'object' && 'code' in resp.data && resp.data.code !== 0) {
      const bizCode = resp.data.code as number;
      const msg = mapErrorMessage(bizCode, resp.data.message || '');
      return Promise.reject({ response: resp, message: msg });
    }
    return resp;
  },
  async (error) => {
    const { config, response } = error;
    if (config && config._authRevision !== useAuthStore.getState().revision) {
      return Promise.reject(new axios.CanceledError('账号已切换'));
    }
    if (response?.status === 401 && config && !config._retry && !config.url?.startsWith('/auth/')) {
      config._retry = true;
      const newToken = await refreshSession();
      if (newToken && config._authRevision === useAuthStore.getState().revision) {
        config.headers.Authorization = `Bearer ${newToken}`;
        return client(config);
      }
      return Promise.reject(error);
    }

    // Map business error code to Chinese message
    if (response?.data?.code) {
      response.data.message = mapErrorMessage(response.data.code, response.data.message);
    }
    return Promise.reject(error);
  },
);

// ─── Helper: unwrap { code, message, data } => data ───
export function unwrap<T>(resp: { data: { data: T } }): T {
  return resp.data.data;
}

export default client;
