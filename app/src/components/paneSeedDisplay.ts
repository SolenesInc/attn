import type { Seed } from '../hooks/useDaemonSocket';
import { seedStateLabel } from './seedStatePresentation';

export type PaneSeedDisplay = (
  | { kind: 'none' }
  | { kind: 'crown'; seedId: string; seed?: Seed }
  | { kind: 'seed'; seed: Seed }
  | { kind: 'plot'; plot: Seed; tended: Seed[] }
  | { kind: 'multi'; tended: Seed[] }
) & { crownSeed?: Seed };

// Tender is cleared on park/harvest/wither: a matching tender_session IS "actively tended".
export function tendedSeeds(seeds: Seed[], sessionId: string): Seed[] {
  if (!sessionId) return [];
  return seeds.filter((seed) => seed.tender_session === sessionId);
}

const MAX_ANCESTRY = 32;

// Capped and cycle-guarded: edges come off the wire and a bad link must not hang the render.
export function plotAncestry(seed: Seed, byId: Map<string, Seed>): string[] {
  const chain: string[] = [];
  const seen = new Set<string>();
  let current: Seed | undefined = seed;
  while (current && chain.length < MAX_ANCESTRY && !seen.has(current.id)) {
    chain.push(current.id);
    seen.add(current.id);
    const parentEdge: { kind: string; to: string } | undefined = current.edges.find((edge) => edge.kind === 'part-of');
    current = parentEdge ? byId.get(parentEdge.to) : undefined;
  }
  return chain;
}

function commonPlot(tended: Seed[], byId: Map<string, Seed>): Seed | undefined {
  const chains = tended.map((seed) => plotAncestry(seed, byId));
  const [first, ...rest] = chains;
  for (const candidate of first) {
    if (rest.every((chain) => chain.includes(candidate))) {
      return byId.get(candidate);
    }
  }
  return undefined;
}

export function derivePaneSeedDisplay(
  seeds: Seed[],
  sessionId: string,
  crownSeedId: string | undefined,
): PaneSeedDisplay {
  const tended = tendedSeeds(seeds, sessionId);
  const crownSeed = crownSeedId ? seeds.find((seed) => seed.id === crownSeedId) : undefined;
  if (tended.length === 0) {
    if (!crownSeedId) return { kind: 'none' };
    return { kind: 'crown', seedId: crownSeedId, seed: crownSeed };
  }
  if (tended.length === 1) return { kind: 'seed', seed: tended[0], crownSeed };
  const byId = new Map(seeds.map((seed) => [seed.id, seed]));
  const plot = commonPlot(tended, byId);
  if (plot) return { kind: 'plot', plot, tended, crownSeed };
  return { kind: 'multi', tended, crownSeed };
}

export interface PaneSeedPopoverRow {
  seed?: Seed;
  seedId: string;
  role: 'plot' | 'tended' | 'crown';
}

interface SeedChipPresentation {
  label: string;
  status: string;
  stateLabel: string;
  aggregate: boolean;
  seedId?: string;
  clickTarget?: string;
  fraction?: string;
}

function singleSeedPresentation(seedId: string, seed?: Seed): SeedChipPresentation {
  const status = seed?.status ?? 'unknown';
  return {
    label: seed?.title.trim() || seedId,
    status,
    stateLabel: seedStateLabel(status),
    aggregate: false,
    seedId,
    clickTarget: seedId,
  };
}

export function seedChipPresentation(display: PaneSeedDisplay): SeedChipPresentation | null {
  switch (display.kind) {
    case 'none': return null;
    case 'crown': return singleSeedPresentation(display.seedId, display.seed);
    case 'seed': return singleSeedPresentation(display.seed.id, display.seed);
    case 'plot': {
      const progress = display.plot.plot_progress;
      const fraction = progress?.total ? `${progress.done}/${progress.total}` : undefined;
      return {
        label: display.plot.title.trim() || display.plot.id,
        status: 'plot',
        stateLabel: fraction ? `${fraction} harvested` : 'Growing',
        aggregate: true,
        seedId: display.plot.id,
        fraction,
      };
    }
    case 'multi': return {
      label: `tending ${display.tended.length}`,
      status: 'multi',
      stateLabel: 'Growing',
      aggregate: true,
    };
  }
}

export function plotStateCounts(seed: Seed): Array<[string, number]> {
  const progress = seed.plot_progress;
  if (!progress) return [];
  return Object.entries({
    growing: progress.growing,
    dormant: progress.dormant,
    harvested: progress.done,
    withered: progress.withered,
    planted: Math.max(0, progress.total - progress.growing - progress.dormant - progress.done - progress.withered),
  }).filter(([, count]) => count > 0);
}

export function popoverRows(display: PaneSeedDisplay, crownSeedId: string | undefined): PaneSeedPopoverRow[] {
  const rows: PaneSeedPopoverRow[] = [];
  switch (display.kind) {
    case 'none':
      return rows;
    case 'crown':
      rows.push({ seed: display.seed, seedId: display.seedId, role: 'crown' });
      return rows;
    case 'seed':
      rows.push({ seed: display.seed, seedId: display.seed.id, role: 'tended' });
      break;
    case 'plot':
      rows.push({ seed: display.plot, seedId: display.plot.id, role: 'plot' });
      for (const seed of display.tended) {
        if (seed.id !== display.plot.id) rows.push({ seed, seedId: seed.id, role: 'tended' });
      }
      break;
    case 'multi':
      for (const seed of display.tended) rows.push({ seed, seedId: seed.id, role: 'tended' });
      break;
  }
  if (crownSeedId && !rows.some((row) => row.seedId === crownSeedId)) {
    rows.push({ seed: display.crownSeed, seedId: crownSeedId, role: 'crown' });
  }
  return rows;
}
