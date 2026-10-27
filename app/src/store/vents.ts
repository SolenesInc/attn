import { create } from 'zustand';
import type { Vent } from '../types/generated';

interface VentsStore {
  vents: Vent[];
  setVents: (vents: Vent[]) => void;
}

export const useVentsStore = create<VentsStore>((set) => ({
  vents: [],
  setVents: (vents) => set({ vents }),
}));
