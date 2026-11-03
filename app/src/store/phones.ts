import { create } from 'zustand';
import type { PhonesUpdatedMessage } from '../types/generated';

interface PhonesStore {
  phones: PhonesUpdatedMessage | null;
  received: (phones: PhonesUpdatedMessage) => void;
}

export const usePhonesStore = create<PhonesStore>((set) => ({
  phones: null,
  received: (phones) => set({ phones }),
}));
