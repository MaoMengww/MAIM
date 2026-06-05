import { create } from 'zustand';

interface UIState {
  sidebarKey: string;
  setSidebarKey: (key: string) => void;
}

export const useUIStore = create<UIState>((set) => ({
  sidebarKey: 'conversations',
  setSidebarKey: (sidebarKey) => set({ sidebarKey }),
}));
