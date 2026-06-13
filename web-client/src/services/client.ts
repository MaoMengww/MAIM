import axios from 'axios';
import { useAuthStore } from '@/stores/auth';
import { safeJsonParse } from '@/utils/json';

const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8080';

const client = axios.create({
  baseURL: `${API_BASE}/api/v1`,
  timeout: 15000,
  transformResponse: [(data: string) => {
    if (typeof data !== 'string') return data;
    try {
      return safeJsonParse(data);
    } catch {
      return data;
    }
  }],
});

// ─── Request: inject JWT ───
client.interceptors.request.use((config) => {
  const token = useAuthStore.getState().token;
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
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
let isRefreshing = false;
let refreshQueue: Array<{
  resolve: (token: string) => void;
  reject: (err: unknown) => void;
}> = [];

async function doRefresh(): Promise<string | null> {
  const rt = useAuthStore.getState().refreshToken;
  if (!rt) return null;
  try {
    const resp = await axios.post<{ code: number; message: string; data: any }>(
      `${API_BASE}/api/v1/auth/refresh`,
      { refresh_token: rt },
    );
    const d = resp.data.data;
    const existingUser = useAuthStore.getState().user;
    useAuthStore.getState().setAuth(d.tokens.access_token, d.tokens.refresh_token, d.user ?? existingUser);
    return d.tokens.access_token;
  } catch {
    useAuthStore.getState().logout();
    return null;
  }
}

client.interceptors.response.use(
  (resp) => {
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
    if (response?.status === 401 && !config._retry) {
      config._retry = true;
      if (!isRefreshing) {
        isRefreshing = true;
        const newToken = await doRefresh();
        isRefreshing = false;
        if (newToken) {
          refreshQueue.forEach((p) => p.resolve(newToken));
          refreshQueue = [];
          config.headers.Authorization = `Bearer ${newToken}`;
          return client(config);
        }
        refreshQueue.forEach((p) => p.reject(new Error('refresh failed')));
        refreshQueue = [];
        return Promise.reject(error);
      }
      return new Promise((resolve, reject) => {
        refreshQueue.push({
          resolve: (token: string) => {
            config.headers.Authorization = `Bearer ${token}`;
            resolve(client(config));
          },
          reject,
        });
      });
    }

    // Map business error code to Chinese message
    if (response?.data?.code) {
      response.data.message = mapErrorMessage(response.data.code, response.data.message);
    }
    return Promise.reject(error);
  },
);

// ─── Helper: unwrap { code, message, data } => data ───
export function unwrap<T>(resp: { data: { data?: T } }): T | null {
  return resp.data?.data ?? null;
}

export default client;
