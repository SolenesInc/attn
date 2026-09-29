import { create } from 'zustand';

export const useDelegationPreferencesPush = create<{
  version: number;
  push: () => void;
}>((set) => ({
  version: 0,
  push: () => set((state) => ({ version: state.version + 1 })),
}));
