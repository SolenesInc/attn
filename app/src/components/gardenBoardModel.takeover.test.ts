import { describe, expect, it } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import { heldByOther, legalVerbs, type ColumnKey, type Verb } from './gardenBoardModel';

function seed(status: string, claim: Pick<Seed, 'tender' | 'claimed'> = { claimed: false }): Seed {
  return {
    body: '',
    created_at: '',
    edges: [],
    gate: false,
    id: 's-7k3f9m',
    planter: { ref: 'user', name: 'the user' },
    profile_id: 'profile-default',
    ready: false,
    rev: 1,
    state_changed_at: '',
    state_changed_at_exact: true,
    status,
    step_slug: '',
    template: false,
    ...claim,
    title: 'a seed',
    updated_at: '',
    vars: [],
  } as Seed;
}

const MOVES: Record<string, Record<ColumnKey, Verb[]>> = {
  planted: { ready: [], growing: [], parked: ['park'], closed: ['harvest', 'wither'] },
  growing: { ready: ['replant'], growing: [], parked: ['park'], closed: ['harvest', 'wither'] },
  dormant: { ready: ['replant'], growing: [], parked: [], closed: ['harvest', 'wither'] },
  harvested: { ready: ['replant'], growing: [], parked: [], closed: [] },
  withered: { ready: ['replant'], growing: [], parked: [], closed: [] },
};

describe('the moves a drag onto a Garden column offers', () => {
  it.each(Object.entries(MOVES).flatMap(([status, columns]) => (
    Object.entries(columns).map(([column, verbs]) => [status, column as ColumnKey, verbs] as const)
  )))('a %s seed dropped on %s offers %j', (status, column, verbs) => {
    expect(legalVerbs(seed(status), column)).toEqual(verbs);
  });
});

describe('who still holds a seed', () => {
  const alder = { ref: 'member:alder', name: 'Alder' };
  const session = { ref: 'session:sess-a', name: 'Garden polish' };

  it.each<[string, Pick<Seed, 'tender' | 'claimed'>, string]>([
    ['a member that is awake', { tender: { ...alder, session_id: 'sess-alder' }, claimed: true }, 'Alder'],
    ['a member that is asleep, since its claim outlives its session', { tender: alder, claimed: true }, 'Alder'],
    ['a session that is alive', { tender: { ...session, session_id: 'sess-a' }, claimed: true }, 'Garden polish'],
    ['a session that has ended', { tender: session, claimed: false }, ''],
    ['a party the daemon could not name', { tender: { ref: 'session:sess-a', name: '', session_id: 'sess-a' }, claimed: true }, 'session:sess-a'],
    ['nobody', { claimed: false }, ''],
  ])('tended by %s', (_name, claim, holder) => {
    expect(heldByOther(seed('growing', claim))).toBe(holder);
  });
});
