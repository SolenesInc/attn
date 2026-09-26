import { describe, expect, it } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import { heldByOther, legalVerbs, type ColumnKey, type Verb } from './gardenBoardModel';

function seed(status: string, tender: { session?: string; member?: string } = {}): Seed {
  return {
    body: '',
    created_at: '',
    edges: [],
    gate: false,
    id: 's-7k3f9m',
    planter_member: '',
    planter_session: '',
    ready: false,
    rev: 1,
    state_changed_at: '',
    state_changed_at_exact: true,
    status,
    step_slug: '',
    template: false,
    tender_member: tender.member ?? '',
    tender_session: tender.session ?? '',
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

describe('who still holds a seed', () => {
  const live = new Set(['sess-a']);

  it.each([
    ['a member whose session is alive', { session: 'sess-a', member: 'alder' }, 'Alder'],
    ['a session that is alive', { session: 'sess-a' }, 'sess-a'],
    ['a member whose session has ended', { session: 'sess-gone', member: 'alder' }, ''],
    ['a session that has ended', { session: 'sess-gone' }, ''],
    ['a member with no session, since attn cannot see a person leave', { member: 'alder' }, 'Alder'],
    ['nobody', {}, ''],
  ])('tended by %s: %j', (_name, tender, holder) => {
    expect(heldByOther(seed('growing', tender), live)).toBe(holder);
  });
});
