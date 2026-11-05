import { create } from 'zustand';
import type { Seed } from '../types/generated';

export function seedParentID(seed: Seed): string {
  return (seed.edges ?? []).find((edge) => edge.kind === 'part-of')?.to ?? '';
}

// A parent missing from the pushed snapshot (it is capped) leaves the known
// seed as a local root rather than hiding it; the guard set survives a cycle.
export function gardenPathToSeed(seeds: Seed[], seedID: string): string[] {
  const byID = new Map(seeds.map((seed) => [seed.id, seed]));
  const path: string[] = [];
  const guard = new Set<string>();
  let cursor = byID.get(seedID);
  while (cursor && !guard.has(cursor.id)) {
    guard.add(cursor.id);
    path.unshift(cursor.id);
    const parentID = seedParentID(cursor);
    cursor = parentID ? byID.get(parentID) : undefined;
  }
  return path;
}

type Update<T> = T | ((prev: T) => T);

function applied<T>(next: Update<T>, prev: T): T {
  return typeof next === 'function' ? (next as (prev: T) => T)(prev) : next;
}

interface GardenWalk {
  trail: string[];
  query: string;
  wideIn: string | null;
  setTrail: (next: Update<string[]>) => void;
  setQuery: (next: Update<string>) => void;
  setWideIn: (next: Update<string | null>) => void;
  arriveAt: (trail: string[]) => void;
}

export const useGardenWalk = create<GardenWalk>((set) => ({
  trail: [],
  query: '',
  wideIn: null,
  setTrail: (next) => set((state) => ({ trail: applied(next, state.trail) })),
  setQuery: (next) => set((state) => ({ query: applied(next, state.query) })),
  setWideIn: (next) => set((state) => ({ wideIn: applied(next, state.wideIn) })),
  arriveAt: (trail) => set({ trail, query: '', wideIn: null }),
}));

// Not store state: a per-wheel-tick write through the reducer would repaint the
// panel.
export const gardenScrollMemory = new Map<string, number>();
