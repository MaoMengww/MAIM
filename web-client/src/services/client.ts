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
  (resp) => resp,
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
    return Promise.reject(error);
  },
);

// ─── Helper: unwrap { code, message, data } => data ───
export function unwrap<T>(resp: { data: { data?: T } }): T | null {
  return resp.data?.data ?? null;
}

export default client;
