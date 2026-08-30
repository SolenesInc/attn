import { create } from 'zustand';
import type { AutoModeState } from '../hooks/daemonAutoModeEvents';

interface AutoModePushStore {
  pushed: AutoModeState | null;
  version: number;
  push: (state: AutoModeState) => void;
  clear: () => void;
}

export const useAutoModePushStore = create<AutoModePushStore>((set) => ({
  pushed: null,
  version: 0,
  push: (state) => set((store) => ({ pushed: state, version: store.version + 1 })),
  clear: () => set({ pushed: null }),
}));
