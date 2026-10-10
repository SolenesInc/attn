import { describe, expect, it } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import { legalVerbs, type ColumnKey, type Verb } from './gardenBoardModel';

function seed(status: string, tender: { session?: string; member?: string } = {}): Seed {
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
    tender: { ref: 'member:' + (tender.member ?? ''), name: tender.member ?? '', session_id: tender.session ?? '' },
    claimed: Boolean(tender.member ?? ''),
    resume_available: false,

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
    expect(legalVerbs(seed(status, { session: 'sess-a' }), column)).toEqual(verbs);
  });
});
