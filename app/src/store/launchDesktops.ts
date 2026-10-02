import { create } from 'zustand';
import type { LaunchDesktopItem } from '../types/generated';

interface LaunchDesktopState {
  items: LaunchDesktopItem[];
  receiveCatalog: (items: LaunchDesktopItem[]) => void;
  receiveItems: (items: LaunchDesktopItem[], profileId: string) => void;
}

export const useLaunchDesktopStore = create<LaunchDesktopState>((set) => ({
  items: [],
  receiveCatalog: (items) => set({ items }),
  receiveItems: (items, profileId) =>
    set((state) => ({
      items: [...state.items.filter((item) => item.profile_id !== profileId), ...items],
    })),
}));
