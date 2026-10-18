import { create } from 'zustand';
import type { CalloutStep } from '../types/generated';

interface Callout {
  requestId: string;
  from: string;
  steps: CalloutStep[];
  index: number;
}

interface CalloutState {
  callout: Callout | null;
  overlayOpen: boolean;
  setOverlayOpen: (open: boolean) => void;
  show: (requestId: string, from: string, steps: CalloutStep[]) => void;
  next: () => void;
  end: () => void;
}

export const useCalloutStore = create<CalloutState>((set) => ({
  callout: null,
  overlayOpen: false,
  setOverlayOpen: (overlayOpen) => set((state) => ({
    overlayOpen,
    callout: overlayOpen && !state.overlayOpen ? null : state.callout,
  })),
  show: (requestId, from, steps) => set({ callout: { requestId, from, steps, index: 0 } }),
  next: () => set(({ callout }) => ({
    callout: callout && callout.index + 1 < callout.steps.length
      ? { ...callout, index: callout.index + 1 } : null,
  })),
  end: () => set({ callout: null }),
}));
