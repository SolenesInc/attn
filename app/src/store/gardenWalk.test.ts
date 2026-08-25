import { describe, expect, it } from 'vitest';
import type { Seed } from '../types/generated';
import { gardenPathToSeed } from './gardenWalk';

function seed(id: string, parent = ''): Seed {
  return {
    id,
    title: id,
    body: '',
    status: 'planted',
    step_slug: id,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: parent ? [{ kind: 'part-of', to: parent }] : [],
    template: false,
    gate: false,
    vars: [],
    ready: true,
    rev: 1,
    created_at: '2026-08-25T08:00:00Z',
    updated_at: '2026-08-25T08:00:00Z',
  };
}

describe('gardenPathToSeed', () => {
  it('returns canonical part-of ancestry from the Garden root', () => {
    const crown = seed('s-crown1');
    const middle = seed('s-mid111', crown.id);
    const leaf = seed('s-leaf11', middle.id);

    expect(gardenPathToSeed([leaf, crown, middle], leaf.id)).toEqual([
      crown.id,
      middle.id,
      leaf.id,
    ]);
  });

  it('treats a seed whose parent missed the pushed snapshot as a known root', () => {
    const leaf = seed('s-leaf11', 's-missed');
    expect(gardenPathToSeed([leaf], leaf.id)).toEqual([leaf.id]);
  });
});
