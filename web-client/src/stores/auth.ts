import { create } from 'zustand';
import type { UserInfo } from '@/types/model';

const STORAGE_KEY = 'aim_auth_v2';

interface StoredAuth {
  token: string;
  refreshToken: string;
  user: UserInfo;
}

function load(): StoredAuth | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch { return null; }
}

function save(token: string | null, refreshToken: string | null, user: UserInfo | null) {
  if (token) {
    // If user is missing, preserve the existing stored user
    if (!user) {
      const existing = load();
      if (existing?.user) {
        user = existing.user;
      }
    }
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
  setAuth: (token: string, refreshToken: string, user: UserInfo) => void;
  setUser: (user: UserInfo) => void;
  logout: () => void;
}

const stored = load();

export const useAuthStore = create<AuthState>((set) => ({
  token: stored?.token ?? null,
  refreshToken: stored?.refreshToken ?? null,
  user: stored?.user ?? null,
  isAuthenticated: !!stored?.token,

  setAuth: (token, refreshToken, user) => {
    save(token, refreshToken, user);
    set({ token, refreshToken, user, isAuthenticated: true });
  },

  setUser: (user) => {
    const s = useAuthStore.getState();
    save(s.token, s.refreshToken, user);
    set({ user });
  },

  logout: () => {
    save(null, null, null);
    set({ token: null, refreshToken: null, user: null, isAuthenticated: false });
  },
}));
