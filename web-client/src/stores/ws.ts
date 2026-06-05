import { create } from 'zustand';

type WSStatus = 'disconnected' | 'connecting' | 'connected';

interface WSState {
  status: WSStatus;
  reconnectAttempts: number;
  reconnectVersion: number;
  setStatus: (s: WSStatus) => void;
  setReconnectAttempts: (n: number) => void;
  bumpReconnectVersion: () => void;
}

export const useWSStore = create<WSState>((set) => ({
  status: 'disconnected',
  reconnectAttempts: 0,
  reconnectVersion: 0,
  setStatus: (status) => set({ status }),
  setReconnectAttempts: (reconnectAttempts) => set({ reconnectAttempts }),
  bumpReconnectVersion: () => set((s) => ({ reconnectVersion: s.reconnectVersion + 1 })),
}));
