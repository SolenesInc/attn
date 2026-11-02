import { create } from 'zustand';
import type { SendersUpdatedMessage } from '../types/generated';

interface SendersStore {
  senders: SendersUpdatedMessage | null;
  received: (senders: SendersUpdatedMessage) => void;
}

export const useSendersStore = create<SendersStore>((set) => ({
  senders: null,
  received: (senders) => set({ senders }),
}));
