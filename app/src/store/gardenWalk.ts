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
