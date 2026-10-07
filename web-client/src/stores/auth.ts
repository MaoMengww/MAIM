import { create } from 'zustand';
import type { UserInfo, TokenPair } from '@/types/model';

const STORAGE_KEY = 'aim_auth_uuid_v1';
const USER_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

interface StoredAuth {
  token: string;
  refreshToken: string;
  user: UserInfo;
}

function load(): StoredAuth | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const auth = JSON.parse(raw);
    return typeof auth.token === 'string' && typeof auth.refreshToken === 'string'
      && typeof auth.user?.id === 'string' && USER_ID.test(auth.user.id) ? auth : null;
  } catch { return null; }
}

function save(token: string | null, refreshToken: string | null, user: UserInfo | null) {
  if (token && refreshToken && user) {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ token, refreshToken, user }));
  } else {
    localStorage.removeItem(STORAGE_KEY);
  }
}

interface AuthState {
  token: string | null;
  refreshToken: string | null;
  user: UserInfo | null;
  isAuthenticated: boolean;
  revision: number;
  setAuth: (token: string, refreshToken: string, user: UserInfo) => void;
  applyRefresh: (revision: number, refreshToken: string, tokens: TokenPair, user?: UserInfo) => boolean;
  setUser: (user: UserInfo) => void;
  logout: () => void;
}

const stored = load();

export const useAuthStore = create<AuthState>((set, get) => ({
  token: stored?.token ?? null,
  refreshToken: stored?.refreshToken ?? null,
  user: stored?.user ?? null,
  isAuthenticated: !!stored,
  revision: 0,

  setAuth: (token, refreshToken, user) => {
    if (!USER_ID.test(user.id)) throw new Error('用户身份无效');
    save(token, refreshToken, user);
    set({ token, refreshToken, user, isAuthenticated: true, revision: get().revision + 1 });
  },

  applyRefresh: (revision, refreshToken, tokens, user) => {
    const current = get();
    if (current.revision !== revision || current.refreshToken !== refreshToken || !current.user) return false;
    if (user && user.id !== current.user.id) throw new Error('刷新返回了不同账号');
    const refreshedUser = user ?? current.user;
    save(tokens.access_token, tokens.refresh_token, refreshedUser);
    set({ token: tokens.access_token, refreshToken: tokens.refresh_token, user: refreshedUser });
    return true;
  },

  setUser: (user) => {
    const current = get();
    if (!current.isAuthenticated || user.id !== current.user?.id) return;
    save(current.token, current.refreshToken, user);
    set({ user });
  },

  logout: () => {
    save(null, null, null);
    set({ token: null, refreshToken: null, user: null, isAuthenticated: false, revision: get().revision + 1 });
  },
}));
