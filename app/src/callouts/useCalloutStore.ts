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
  show: (requestId: string, from: string, steps: CalloutStep[]) => void;
  next: () => void;
  end: () => void;
}

export const useCalloutStore = create<CalloutState>((set) => ({
  callout: null,
  show: (requestId, from, steps) => set({ callout: { requestId, from, steps, index: 0 } }),
  next: () => set(({ callout }) => ({
    callout: callout && callout.index + 1 < callout.steps.length
      ? { ...callout, index: callout.index + 1 } : null,
  })),
  end: () => set({ callout: null }),
}));
