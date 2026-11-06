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
  keysReleased: boolean;
  setOverlayOpen: (open: boolean) => void;
  show: (requestId: string, from: string, steps: CalloutStep[]) => void;
  next: () => void;
  clickedOutside: () => void;
  clickedCard: () => void;
  end: () => void;
}

const isTour = (callout: Callout | null) => (callout?.steps.length ?? 0) > 1;

export const useCalloutStore = create<CalloutState>((set) => ({
  callout: null,
  overlayOpen: false,
  keysReleased: false,
  setOverlayOpen: (overlayOpen) => set((state) => ({
    overlayOpen,
    callout: overlayOpen && !state.overlayOpen && !isTour(state.callout) ? null : state.callout,
  })),
  show: (requestId, from, steps) => set({ callout: { requestId, from, steps, index: 0 }, keysReleased: false }),
  next: () => set(({ callout }) => ({
    callout: callout && callout.index + 1 < callout.steps.length
      ? { ...callout, index: callout.index + 1 } : null,
  })),
  clickedOutside: () => set(({ callout }) => (isTour(callout) ? { keysReleased: true } : { callout: null })),
  clickedCard: () => set({ keysReleased: false }),
  end: () => set({ callout: null }),
}));
