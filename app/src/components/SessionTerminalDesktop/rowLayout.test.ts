import { describe, expect, it } from 'vitest';
import { collectLayoutLeaves, getNormalizedPaneBounds, leafSlotId, type TerminalLayoutNode } from '../../types/desktop';
import {
  readRow,
  resolveRowLayout,
  stepNarrowestCols,
  type FirstColumn,
  type RowLayoutInput,
} from './rowLayout';

const PX_PER_COL = 10;
const NARROWEST = 500;

type Spec = string | { id: string; wanted?: number; below?: string };

function leaf(spec: Spec): TerminalLayoutNode {
  if (typeof spec === 'string') return { type: 'pane', paneId: spec };
  const top: TerminalLayoutNode = { type: 'pane', paneId: spec.id, wantedCols: spec.wanted };
  return spec.below
    ? { type: 'split', splitId: `stack-${spec.id}`, direction: 'horizontal', ratio: 0.5, children: [top, { type: 'pane', paneId: spec.below }] }
    : top;
}

function rightChain(specs: readonly Spec[]): TerminalLayoutNode {
  const [first, ...rest] = specs;
  return rest.length === 0
    ? leaf(first)
    : { type: 'split', splitId: `s-${specs.length}`, direction: 'vertical', ratio: 0.5, children: [leaf(first), rightChain(rest)] };
}

function resolve(specs: readonly Spec[], options: Partial<RowLayoutInput> & { activeLeafId: string }) {
  return resolveRowLayout({
    tree: rightChain(specs),
    firstColumn: null,
    areaPx: 1500,
    narrowestPx: NARROWEST,
    pxPerCol: PX_PER_COL,
    ...options,
  });
}

const firsts = (columns: { leafIds: string[] }[]) => columns.map((column) => column.leafIds[0]);
const ROW = ['a', 'b', 'c', 'd', 'e', 'f'];

function widthsPx(plan: ReturnType<typeof resolveRowLayout>, areaPx: number) {
  const bounds = getNormalizedPaneBounds(plan.renderedTree);
  return firsts(plan.screen).map((id) => Math.round(bounds.get(id)!.width * areaPx));
}

function seeded(seed: number) {
  let state = seed;
  return (below: number) => {
    state = (state + 0x6d2b79f5) | 0;
    let mixed = Math.imul(state ^ (state >>> 15), 1 | state);
    mixed = (mixed + Math.imul(mixed ^ (mixed >>> 7), 61 | mixed)) ^ mixed;
    return ((mixed ^ (mixed >>> 14)) >>> 0) % below;
  };
}

describe('readRow', () => {
  it('reads top-level vertical splits as columns and everything else as one column', () => {
    const tree: TerminalLayoutNode = {
      type: 'split',
      splitId: 'root',
      direction: 'vertical',
      ratio: 0.5,
      children: [
        { type: 'split', splitId: 'left', direction: 'vertical', ratio: 0.5, children: [leaf('a'), leaf({ id: 'b', below: 'c' })] },
        leaf({ id: 'd', wanted: 80 }),
      ],
    };
    expect(readRow(tree).map((column) => column.leafIds)).toEqual([['a'], ['b', 'c'], ['d']]);
    expect(readRow(tree)[2].wantedCols).toBe(80);
  });
});

describe('resolveRowLayout', () => {
  it.each<[string, { active: string; first: FirstColumn | null; area: number }, string[], string[], string[]]>([
    ['starts at the row start', { active: 'a', first: null, area: 1500 }, ['a', 'b', 'c'], [], ['d', 'e', 'f']],
    ['slides right until the active tile shows', { active: 'd', first: { leafId: 'a', index: 0 }, area: 1000 }, ['c', 'd'], ['a', 'b'], ['e', 'f']],
    ['slides left to the active tile', { active: 'b', first: { leafId: 'c', index: 2 }, area: 1000 }, ['b', 'c'], ['a'], ['d', 'e', 'f']],
    ['keeps the first column while the active tile is on screen', { active: 'e', first: { leafId: 'd', index: 3 }, area: 1000 }, ['d', 'e'], ['a', 'b', 'c'], ['f']],
    ['back-fills when the row end leaves room', { active: 'f', first: { leafId: 'f', index: 5 }, area: 1500 }, ['d', 'e', 'f'], ['a', 'b', 'c'], []],
    ['does not back-fill without room for a whole column', { active: 'f', first: { leafId: 'f', index: 5 }, area: 999 }, ['f'], ['a', 'b', 'c', 'd', 'e'], []],
    ['a closed first column falls back by index', { active: 'e', first: { leafId: 'closed', index: 3 }, area: 1000 }, ['d', 'e'], ['a', 'b', 'c'], ['f']],
    ['a closed first column past the row end lands on the active tile', { active: 'c', first: { leafId: 'closed', index: 40 }, area: 1000 }, ['c', 'd'], ['a', 'b'], ['e', 'f']],
    ['shows one column when the area is too small for it', { active: 'c', first: null, area: 100 }, ['c'], ['a', 'b'], ['d', 'e', 'f']],
  ])('%s', (_name, { active, first, area }, screen, before, after) => {
    const plan = resolve(ROW, { activeLeafId: active, firstColumn: first, areaPx: area });
    expect(firsts(plan.screen)).toEqual(screen);
    expect(firsts(plan.before)).toEqual(before);
    expect(firsts(plan.after)).toEqual(after);
    expect(plan.firstColumn).toEqual({ leafId: screen[0], index: ROW.indexOf(screen[0]) });
    expect(plan.suspendedLeafIds.size).toBe(0);
  });

  it.each<[string, Spec[], number, number[]]>([
    ['shares equally without wanted widths', ['a', 'b', 'c'], 1800, [600, 600, 600]],
    ['keeps wanted cols and shares the rest', [{ id: 'a', wanted: 80 }, 'b', 'c'], 1800, [800, 500, 500]],
    ['scales when every column is wanted', [{ id: 'a', wanted: 100 }, { id: 'b', wanted: 50 }], 1800, [1200, 600]],
    ['treats a wanted width under the narrowest as the narrowest', [{ id: 'a', wanted: 10 }, 'b'], 1000, [500, 500]],
    ['scales a single wanted column wider than the area', [{ id: 'a', wanted: 300 }], 900, [900]],
  ])('%s', (_name, specs, areaPx, widths) => {
    const plan = resolve(specs, { activeLeafId: 'a', areaPx });
    expect(widthsPx(plan, areaPx)).toEqual(widths);
  });

  it('counts a wanted width when deciding what fits', () => {
    const plan = resolve([{ id: 'a', wanted: 120 }, 'b', 'c'], { activeLeafId: 'a', areaPx: 1700 });
    expect(firsts(plan.screen)).toEqual(['a', 'b']);
    expect(firsts(resolve([{ id: 'a', wanted: 120 }, 'b', 'c'], { activeLeafId: 'a', areaPx: 1699 }).screen)).toEqual(['a']);
  });

  it('keeps a stack whole and renders its real split with a divider, but none between columns', () => {
    const plan = resolve(['a', { id: 'b', below: 'c' }, 'd'], { activeLeafId: 'c', areaPx: 1000 });
    expect(plan.screen.map((column) => column.leafIds)).toEqual([['a'], ['b', 'c']]);
    expect(plan.dividers.map((divider) => divider.splitId)).toEqual(['stack-b']);
    expect(collectLayoutLeaves(plan.renderedTree).map(leafSlotId)).toEqual(['a', 'b', 'c']);
  });

  it('zooms within the screen, and focuses a single leaf, without moving the screen', () => {
    const zoomed = resolve(ROW, { activeLeafId: 'b', areaPx: 1000, view: { mode: 'zoomed', leafId: 'b' } });
    expect(firsts(zoomed.screen)).toEqual(['a', 'b']);
    const bounds = getNormalizedPaneBounds(zoomed.renderedTree);
    expect([...bounds.keys()]).toEqual(['a', 'b']);
    expect(bounds.get('b')!.width).toBeCloseTo(0.76);
    expect(zoomed.dividers).toEqual([]);

    const focused = resolve(ROW, { activeLeafId: 'b', areaPx: 1000, view: { mode: 'focused', leafId: 'b' } });
    expect(focused.renderedTree).toEqual({ type: 'pane', paneId: 'b' });
    expect(firsts(focused.screen)).toEqual(['a', 'b']);
  });

  it('holds its promises over generated rows, windows and first columns', () => {
    for (let seed = 1; seed <= 300; seed += 1) {
      const random = seeded(seed);
      const specs: Spec[] = Array.from({ length: 1 + random(14) }, (_, index) => {
        const id = `t${index}`;
        const below = random(5) === 0 ? `${id}-below` : undefined;
        return { id, wanted: random(4) === 0 ? 40 + random(120) : undefined, below };
      });
      const tree = rightChain(specs);
      const leaves = collectLayoutLeaves(tree).map(leafSlotId);
      const activeLeafId = leaves[random(leaves.length)];
      const firstLeaf = leaves[random(leaves.length)];
      const firstColumn: FirstColumn = { leafId: random(3) === 0 ? 'closed' : firstLeaf, index: random(20) };
      const narrowestPx = 480 + random(800);
      const input = { tree, activeLeafId, firstColumn, narrowestPx, pxPerCol: PX_PER_COL };
      const at = (areaPx: number) => resolveRowLayout({ ...input, areaPx });

      const sweep: number[] = [];
      for (let area = 3000; area >= 300; area -= 137) sweep.push(area);
      const down = sweep.map((area) => firsts(at(area).screen));
      const back = [...sweep].reverse().map((area) => firsts(at(area).screen)).reverse();
      expect(back).toEqual(down);

      for (const area of sweep) {
        const plan = at(area);
        const context = `seed ${seed} area ${area}`;
        const shown = plan.screen.flatMap((column) => column.leafIds);
        expect(shown, context).toContain(activeLeafId);
        const rowIds = readRow(tree).map((column) => column.leafIds[0]);
        expect([...plan.before, ...plan.screen, ...plan.after].map((column) => column.leafIds[0]), context).toEqual(rowIds);

        const need = (column: { wantedCols?: number }) => Math.max(narrowestPx, (column.wantedCols ?? 0) * PX_PER_COL);
        const used = plan.screen.reduce((sum, column) => sum + need(column), 0);
        if (plan.screen.length > 1) expect(used, context).toBeLessThanOrEqual(area + 0.5);
        const previous = plan.before[plan.before.length - 1];
        if (previous) expect(used + need(previous), `${context} back-fill`).toBeGreaterThan(area + 0.5);

        const bounds = getNormalizedPaneBounds(plan.renderedTree);
        const widths = plan.screen.map((column) => bounds.get(column.leafIds[0])!.width * area);
        expect(widths.reduce((sum, width) => sum + width, 0), context).toBeCloseTo(area, 3);
        plan.screen.forEach((column, index) => {
          if (plan.screen.every((each) => each.wantedCols)) {
            expect(widths[index] / need(column), context).toBeCloseTo(area / used, 3);
          } else if (column.wantedCols) {
            expect(widths[index], context).toBeCloseTo(need(column), 3);
          } else {
            expect(widths[index], context).toBeGreaterThanOrEqual(Math.min(narrowestPx, area) - 0.001);
          }
        });
      }
    }
  });
});

describe('stepNarrowestCols', () => {
  const base = { tree: rightChain(ROW), activeLeafId: 'a', firstColumn: null, pxPerCol: PX_PER_COL };

  it('steps until one fewer or one more tile fits', () => {
    expect(stepNarrowestCols({ ...base, areaPx: 2100 }, 66, 'fewer', 2100)).toEqual({ kind: 'step', cols: 76 });
    expect(stepNarrowestCols({ ...base, areaPx: 2100 }, 76, 'more', 2100)).toEqual({ kind: 'step', cols: 66 });
    expect(stepNarrowestCols({ ...base, areaPx: 1200 }, 57, 'fewer', 1200)).toEqual({ kind: 'step', cols: 66 });
  });

  it('says the numbers when no step shows another tile', () => {
    expect(stepNarrowestCols({ ...base, areaPx: 1000 }, 57, 'more', 1100)).toEqual({
      kind: 'limit',
      message: '2 tiles at the narrowest step (57 cols, 570px) need 1140px. Tiles have 1000px here.',
    });
    expect(stepNarrowestCols({ ...base, areaPx: 1000 }, 57, 'more', 1240)).toEqual({
      kind: 'limit',
      message: '2 tiles at the narrowest step (57 cols, 570px) need 1140px. Tiles have 1000px here. Closing the sidebar would make room.',
    });
    expect(stepNarrowestCols({ ...base, areaPx: 500 }, 66, 'fewer', 500)).toEqual({ kind: 'limit', message: 'Already showing one tile.' });
    expect(stepNarrowestCols({ ...base, areaPx: 3100 }, 150, 'fewer', 3100)).toEqual({
      kind: 'limit',
      message: '2 tiles still fit at the widest step (150 cols, 1500px). Tiles have 3100px here.',
    });
    expect(stepNarrowestCols({ ...base, tree: rightChain(['a', 'b']), areaPx: 2000 }, 66, 'more', 2000)).toEqual({
      kind: 'limit',
      message: 'Every tile on this desktop is already on screen.',
    });
  });
});
