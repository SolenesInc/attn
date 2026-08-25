import { create } from 'zustand';

interface GardenWalk {
  trail: string[];
  setTrail: (next: string[] | ((prev: string[]) => string[])) => void;
}

export const useGardenWalk = create<GardenWalk>((set) => ({
  trail: [],
  setTrail: (next) =>
    set((state) => ({ trail: typeof next === 'function' ? next(state.trail) : next })),
}));

// Not store state: a per-wheel-tick write through the reducer would repaint the
// panel.
export const gardenScrollMemory = new Map<string, number>();
