import { describe, expect, it } from 'vitest';
import type { Seed } from '../hooks/useDaemonSocket';
import { derivePaneSeedDisplay, popoverRows } from './paneSeedDisplay';

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

describe('derivePaneSeedDisplay', () => {
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
    const rows = popoverRows({ kind: 'seed', seed: tended }, 's-crown1');
    expect(rows.map((row) => [row.seedId, row.role])).toEqual([
      ['s-work11', 'tended'],
      ['s-crown1', 'crown'],
    ]);
  });

  it('heads a plot display with the plot row', () => {
    const plot = seed({ id: 's-plot11', title: 'the arc' });
    const a = seed({ id: 's-a', title: 'a', tender_session: SESSION });
    const rows = popoverRows({ kind: 'plot', plot, tended: [a] }, undefined);
    expect(rows.map((row) => row.role)).toEqual(['plot', 'tended']);
  });
});
