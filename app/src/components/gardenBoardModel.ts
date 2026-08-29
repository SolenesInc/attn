import type { Seed } from '../hooks/useDaemonSocket';
import { crewHolderName } from '../utils/crewName';

export type ColumnKey = 'ready' | 'growing' | 'parked' | 'closed';

export type Verb = 'park' | 'harvest' | 'wither' | 'replant';

const CLOSED = new Set(['harvested', 'withered']);

export function tenderOf(seed: Seed): string {
  return crewHolderName(seed.tender_member, seed.tender_session);
}

export function heldByOther(seed: Seed, liveSessions: Set<string>): string {
  if (!seed.tender_session && !seed.tender_member) return '';
  if (seed.tender_session && !liveSessions.has(seed.tender_session)) return '';
  return tenderOf(seed);
}

export function columnOf(seed: Seed): ColumnKey {
  if (CLOSED.has(seed.status)) return 'closed';
  if (seed.status === 'dormant') return 'parked';
  if (seed.status === 'growing') return 'growing';
  return 'ready';
}

// The garden's own lifecycle table (internal/garden/lifecycle.go) read from the board's side.
export function legalVerbs(seed: Seed, target: ColumnKey): Verb[] {
  const status = seed.status;
  const open = status === 'planted' || status === 'growing' || status === 'dormant';
  switch (target) {
    case 'closed':
      return open ? ['harvest', 'wither'] : [];
    case 'parked':
      return status === 'planted' || status === 'growing' ? ['park'] : [];
    case 'ready':
      return status === 'planted' ? [] : ['replant'];
    case 'growing':
      return [];
  }
}

// The verbs a card offers on its own. The keyboard path and the drag path must never disagree,
// so they read the same table.
export function verbsFor(seed: Seed): Verb[] {
  const columns: ColumnKey[] = ['growing', 'parked', 'closed', 'ready'];
  return columns.flatMap((column) => legalVerbs(seed, column));
}

interface VerbSpec {
  label: string;
  prompt: string;
  required: boolean;
  // A reason is stored on the seed by harvest and wither alone; the other moves refuse one.
  reasonOnSeed: boolean;
}

export const VERBS: Record<Verb, VerbSpec> = {
  harvest: {
    label: 'Harvest',
    prompt: 'what got done',
    required: true,
    reasonOnSeed: true,
  },
  wither: {
    label: 'Wither',
    prompt: 'why nobody should pick this up',
    required: false,
    reasonOnSeed: true,
  },
  park: {
    label: 'Park',
    prompt: 'what you are leaving it at',
    required: false,
    reasonOnSeed: false,
  },
  replant: {
    label: 'Replant',
    prompt: 'why it is open again',
    required: false,
    reasonOnSeed: false,
  },
};
