import { describe, expect, it } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import { derivePaneSeedDisplay, popoverRows, tendedSeeds } from './paneSeedDisplay';

function seed(overrides: Partial<Seed> & { id: string; title: string }): Seed {
  return {
    body: '',
    status: 'growing',
    state_changed_at: new Date().toISOString(),
    state_changed_at_exact: true,
    step_slug: overrides.title,
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    ready: false,
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  };
}

const SESSION = 'sess-a';

describe('tendedSeeds', () => {
  const memberClaim = seed({ id: 's-member', title: 'Member work', tender_member: 'fern' });
  const sessionClaim = seed({ id: 's-session', title: 'Session work', tender_session: SESSION, tender_member: 'fern' });
  const otherClaim = seed({ id: 's-other', title: 'Other work', tender_member: 'oak' });

  it('includes member-only and session claims once each for the active crew session', () => {
    expect(tendedSeeds([memberClaim, sessionClaim, otherClaim], SESSION, 'fern')).toEqual([memberClaim, sessionClaim]);
  });

  it('carries member-only claims into a new session without carrying an old session claim', () => {
    expect(tendedSeeds([memberClaim, sessionClaim], 'sess-new', 'fern')).toEqual([memberClaim]);
  });

  it('does not lend member claims to ordinary sessions or an absent session', () => {
    expect(tendedSeeds([memberClaim, sessionClaim], SESSION)).toEqual([sessionClaim]);
    expect(tendedSeeds([memberClaim], '', 'fern')).toEqual([]);
  });

  it('drops a member claim when its tender is cleared on release', () => {
    for (const status of ['dormant', 'harvested', 'withered']) {
      expect(tendedSeeds([{ ...memberClaim, status, tender_member: '' }], SESSION, 'fern')).toEqual([]);
    }
  });
});

describe('derivePaneSeedDisplay', () => {
  it('uses the existing multi-seed presentation for crew claims and deduplicates the reporting seed', () => {
    const first = seed({ id: 's-first', title: 'First task', tender_member: 'fern' });
    const second = seed({ id: 's-second', title: 'Second task', tender_member: 'fern' });
    const display = derivePaneSeedDisplay([first, second], SESSION, first.id, 'fern');
    expect(display.kind).toBe('multi');
    expect(popoverRows(display, first.id).map((row) => row.seedId)).toEqual([first.id, second.id]);
  });

  it('is none with no crown and nothing tended', () => {
    expect(derivePaneSeedDisplay([], SESSION, undefined)).toEqual({ kind: 'none' });
  });

  it('falls back to the crown when dispatched but tending nothing', () => {
    const crown = seed({ id: 's-crown1', title: 'the plan' });
    const display = derivePaneSeedDisplay([crown], SESSION, 's-crown1');
    expect(display).toEqual({ kind: 'crown', seedId: 's-crown1', seed: crown });
  });

  it('keeps the crown fallback even when the crown seed is not in the list', () => {
    expect(derivePaneSeedDisplay([], SESSION, 's-gone11')).toEqual({
      kind: 'crown',
      seedId: 's-gone11',
      seed: undefined,
    });
  });

  it('shows the one tended seed over the crown', () => {
    const tended = seed({ id: 's-work11', title: 'move the wire', tender_session: SESSION });
    const display = derivePaneSeedDisplay([tended], SESSION, 's-crown1');
    expect(display).toEqual({ kind: 'seed', seed: tended });
  });

  it('collapses tended siblings to their plot', () => {
    const plot = seed({ id: 's-plot11', title: 'the arc' });
    const a = seed({ id: 's-a', title: 'a', tender_session: SESSION, edges: [{ kind: 'part-of', to: 's-plot11' }] });
    const b = seed({ id: 's-b', title: 'b', tender_session: SESSION, edges: [{ kind: 'part-of', to: 's-plot11' }] });
    const display = derivePaneSeedDisplay([plot, a, b], SESSION, undefined);
    expect(display.kind).toBe('plot');
    if (display.kind === 'plot') {
      expect(display.plot.id).toBe('s-plot11');
      expect(display.tended.map((entry) => entry.id)).toEqual(['s-a', 's-b']);
    }
  });

  it('uses a tended seed itself as the plot when the rest sit under it', () => {
    const parent = seed({ id: 's-plot11', title: 'the arc', tender_session: SESSION });
    const child = seed({ id: 's-a', title: 'a', tender_session: SESSION, edges: [{ kind: 'part-of', to: 's-plot11' }] });
    const display = derivePaneSeedDisplay([parent, child], SESSION, undefined);
    expect(display.kind).toBe('plot');
    if (display.kind === 'plot') expect(display.plot.id).toBe('s-plot11');
  });

  it('goes multi when tended seeds share no plot', () => {
    const a = seed({ id: 's-a', title: 'a', tender_session: SESSION });
    const b = seed({ id: 's-b', title: 'b', tender_session: SESSION });
    expect(derivePaneSeedDisplay([a, b], SESSION, undefined).kind).toBe('multi');
  });

  it('survives a part-of cycle', () => {
    const a = seed({ id: 's-a', title: 'a', tender_session: SESSION, edges: [{ kind: 'part-of', to: 's-b' }] });
    const b = seed({ id: 's-b', title: 'b', tender_session: SESSION, edges: [{ kind: 'part-of', to: 's-a' }] });
    const display = derivePaneSeedDisplay([a, b], SESSION, undefined);
    expect(display.kind).toBe('plot');
  });
});

describe('popoverRows', () => {
  it('appends the crown as context when it is not among the tended rows', () => {
    const tended = seed({ id: 's-work11', title: 'move the wire', tender_session: SESSION });
    const crown = seed({ id: 's-crown1', title: 'the plan', status: 'dormant' });
    const display = derivePaneSeedDisplay([tended, crown], SESSION, crown.id);
    const rows = popoverRows(display, crown.id);
    expect(rows.map((row) => [row.seedId, row.role])).toEqual([
      ['s-work11', 'tended'],
      ['s-crown1', 'crown'],
    ]);
    expect(rows[1].seed).toEqual(crown);
  });

  it('heads a plot display with the plot row', () => {
    const plot = seed({ id: 's-plot11', title: 'the arc' });
    const a = seed({ id: 's-a', title: 'a', tender_session: SESSION });
    const rows = popoverRows({ kind: 'plot', plot, tended: [a] }, undefined);
    expect(rows.map((row) => row.role)).toEqual(['plot', 'tended']);
  });
});
