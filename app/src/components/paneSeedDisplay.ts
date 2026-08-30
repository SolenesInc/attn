import type { Seed } from '../hooks/useDaemonSocket';

export type PaneSeedDisplay =
  | { kind: 'none' }
  | { kind: 'crown'; seedId: string; seed?: Seed }
  | { kind: 'seed'; seed: Seed }
  | { kind: 'plot'; plot: Seed; tended: Seed[] }
  | { kind: 'multi'; tended: Seed[] };

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
  if (tended.length === 0) {
    if (!crownSeedId) return { kind: 'none' };
    return { kind: 'crown', seedId: crownSeedId, seed: seeds.find((seed) => seed.id === crownSeedId) };
  }
  if (tended.length === 1) return { kind: 'seed', seed: tended[0] };
  const byId = new Map(seeds.map((seed) => [seed.id, seed]));
  const plot = commonPlot(tended, byId);
  if (plot) return { kind: 'plot', plot, tended };
  return { kind: 'multi', tended };
}

export interface PaneSeedPopoverRow {
  seed?: Seed;
  seedId: string;
  role: 'plot' | 'tended' | 'crown';
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
    rows.push({ seedId: crownSeedId, role: 'crown' });
  }
  return rows;
}
