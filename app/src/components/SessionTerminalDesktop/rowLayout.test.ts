import { describe, expect, it } from 'vitest';
import { collectLayoutLeaves, getNormalizedPaneBounds, leafSlotId, type TerminalLayoutNode } from '../../types/desktop';
import {
  applyColumnDrag,
  columnDividerIndex,
  planSlotSwap,
  readRow,
  resolveRowLayout,
  reachableTileCounts,
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

const closeTo = (actual: number, expected: number) => Math.abs(actual - expected) < 0.0005;

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
    ['gives the area to a single wanted column wider than it', [{ id: 'a', wanted: 300 }], 900, [900]],
    ['shares the area when a wanted width is wider than it', [{ id: 'a', wanted: 300 }, 'b'], 1200, [600, 600]],
  ])('%s', (_name, specs, areaPx, widths) => {
    const plan = resolve(specs, { activeLeafId: 'a', areaPx });
    expect(widthsPx(plan, areaPx)).toEqual(widths);
  });

  it('counts a wanted width when deciding what fits', () => {
    const plan = resolve([{ id: 'a', wanted: 120 }, 'b', 'c'], { activeLeafId: 'a', areaPx: 1700 });
    expect(firsts(plan.screen)).toEqual(['a', 'b']);
    expect(firsts(resolve([{ id: 'a', wanted: 120 }, 'b', 'c'], { activeLeafId: 'a', areaPx: 1699 }).screen)).toEqual(['a']);
  });

  it('keeps a stack whole and renders its real split with a divider, and one between columns', () => {
    const plan = resolve(['a', { id: 'b', below: 'c' }, 'd'], { activeLeafId: 'c', areaPx: 1000 });
    expect(plan.screen.map((column) => column.leafIds)).toEqual([['a'], ['b', 'c']]);
    expect(plan.dividers.map((divider) => divider.splitId)).toEqual(['row:a', 'stack-b']);
    expect(columnDividerIndex(plan.screen, 'row:a')).toBe(0);
    expect(columnDividerIndex(plan.screen, 'stack-b')).toBe(-1);
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

      const rowIds = readRow(tree).map((column) => column.leafIds[0]);
      const inEffect = (column: { wantedCols?: number }, area: number) => (column.wantedCols ?? 0) * PX_PER_COL <= area;
      const need = (column: { wantedCols?: number }, area: number) =>
        Math.max(narrowestPx, inEffect(column, area) ? (column.wantedCols ?? 0) * PX_PER_COL : 0);
      const broken: string[] = [];
      for (const area of sweep) {
        const plan = at(area);
        const promise = (kept: boolean, name: string) => {
          if (!kept) broken.push(`seed ${seed} area ${area}: ${name}`);
        };
        promise(plan.screen.some((column) => column.leafIds.includes(activeLeafId)), 'the active tile is on screen');
        const partition = [...plan.before, ...plan.screen, ...plan.after].map((column) => column.leafIds[0]);
        promise(partition.join() === rowIds.join(), 'before, screen and after are the row in order');

        const used = plan.screen.reduce((sum, column) => sum + need(column, area), 0);
        promise(plan.screen.length === 1 || used <= area + 0.5, 'several columns fit the area');
        const previous = plan.before[plan.before.length - 1];
        promise(!previous || used + need(previous, area) > area + 0.5, 'no earlier column would still fit');

        const bounds = getNormalizedPaneBounds(plan.renderedTree);
        const widths = plan.screen.map((column) => bounds.get(column.leafIds[0])!.width * area);
        promise(closeTo(widths.reduce((sum, width) => sum + width, 0), area), 'widths fill the area');
        const everyColumnWanted = plan.screen.every((column) => column.wantedCols && inEffect(column, area));
        plan.screen.forEach((column, index) => {
          const name = `width of ${column.leafIds[0]}`;
          if (everyColumnWanted) promise(closeTo(widths[index] / need(column, area), area / used), `${name} scales with the others`);
          else if (column.wantedCols && inEffect(column, area)) promise(closeTo(widths[index], need(column, area)), `${name} is its wanted width`);
          else promise(widths[index] >= Math.min(narrowestPx, area) - 0.001, `${name} is at least the narrowest`);
        });
      }
      expect(broken).toEqual([]);
    }
  });
});

describe('applyColumnDrag', () => {
  const start = (specs: readonly Spec[], activeLeafId: string, areaPx = 1800) => {
    const plan = resolve(specs, { activeLeafId, areaPx });
    return { start: { screen: plan.screen, widths: plan.widths, firstColumn: plan.firstColumn }, activeLeafId };
  };
  const drag = (specs: readonly Spec[], activeLeafId: string, divider: number, distance: number) => {
    const from = start(specs, activeLeafId);
    return applyColumnDrag(from.start, divider, distance, { activeLeafId, areaPx: 1800, narrowestPx: NARROWEST, pxPerCol: PX_PER_COL });
  };
  const wanted = (result: { wantedCols: ReadonlyMap<string, number> }) => Object.fromEntries(result.wantedCols);

  it.each<[string, Spec[], string, number, number, Record<string, number>, string, boolean]>([
    ['takes room from the neighbour on a right drag', ['a', 'b', 'c'], 'a', 0, 50, { a: 65, b: 55 }, 'a', false],
    ['takes room from the neighbour on a left drag', ['a', 'b', 'c'], 'a', 1, -50, { c: 65, b: 55 }, 'a', false],
    ['then from the next neighbour', ['a', 'b', 'c'], 'a', 0, 150, { a: 75, b: 50, c: 55 }, 'a', false],
    ['the far column leaves past the narrowest, and its room goes to the next one', ['a', 'b', 'c'], 'a', 0, 250, { a: 85, b: 95 }, 'a', false],
    ['stops at the narrowest instead of shelving the active column', ['a', 'b', 'c'], 'c', 0, 1000, { a: 80, b: 50, c: 50 }, 'a', true],
    ['stops when the active column is the neighbour', ['a', 'b', 'c'], 'b', 0, 1000, { a: 130, b: 50 }, 'a', true],
    ['grows the active column until nothing is left to give', ['a', 'b'], 'a', 0, 5000, { a: 180 }, 'a', true],
    ['a left drag moves the first column when the far left column leaves', ['a', 'b', 'c'], 'c', 1, -250, { b: 95, c: 85 }, 'b', false],
    ['never shelves the active column on a left drag', ['a', 'b', 'c'], 'a', 1, -1000, { c: 80, b: 50, a: 50 }, 'a', true],
  ])('%s', (_name, specs, activeLeafId, divider, distance, expected, first, stopped) => {
    const result = drag(specs, activeLeafId, divider, distance);
    expect(wanted(result)).toEqual(expected);
    expect(result.firstColumn.leafId).toBe(first);
    expect(result.stopped).toBe(stopped);
  });

  it('dragging back to the start restores everything, whatever the path', () => {
    const specs: Spec[] = ['a', { id: 'b', wanted: 70 }, 'c'];
    const from = start(specs, 'b');
    const measures = { activeLeafId: 'b', areaPx: 1800, narrowestPx: NARROWEST, pxPerCol: PX_PER_COL };
    for (const distance of [40, 400, -400, 5000]) applyColumnDrag(from.start, 0, distance, measures);
    const back = applyColumnDrag(from.start, 0, 0, measures);
    expect(back.wantedCols.size).toBe(0);
    expect(back.firstColumn).toEqual(from.start.firstColumn);
    expect(back.stopped).toBe(false);
  });

  it('follows the pointer and leaves other columns alone when every wanted width is scaled', () => {
    for (let seed = 1; seed <= 300; seed += 1) {
      const random = seeded(seed);
      const specs: Spec[] = Array.from({ length: 2 + random(3) }, (_, index) => ({ id: `t${index}`, wanted: 50 + random(40) }));
      const needed = specs.reduce((sum, spec) => sum + (spec as { wanted: number }).wanted * PX_PER_COL, 0);
      const input = { tree: rightChain(specs), activeLeafId: 't0', firstColumn: null, areaPx: needed + random(800), narrowestPx: NARROWEST, pxPerCol: PX_PER_COL };
      const plan = resolveRowLayout(input);
      const divider = random(plan.screen.length - 1);
      const distance = random(41) - 20;
      const measures = { activeLeafId: 't0', areaPx: input.areaPx, narrowestPx: NARROWEST, pxPerCol: PX_PER_COL };
      const result = applyColumnDrag({ screen: plan.screen, widths: plan.widths, firstColumn: plan.firstColumn }, divider, distance, measures);
      const dragged = resolveRowLayout({ ...input, firstColumn: result.firstColumn, pendingWantedCols: result.wantedCols });

      expect(dragged.screen.length).toBe(plan.screen.length);
      const edge = (widths: readonly number[]) => widths.slice(0, divider + 1).reduce((sum, width) => sum + width, 0);
      if (result.wantedCols.size > 0) expect(Math.abs(edge(dragged.widths) - edge(plan.widths) - distance)).toBeLessThanOrEqual(2 * PX_PER_COL);
      dragged.widths.forEach((width, index) => {
        if (index !== divider && index !== divider + 1 && result.wantedCols.size > 0) expect(Math.abs(width - plan.widths[index])).toBeLessThanOrEqual(2 * PX_PER_COL);
      });
    }
  });

  it('holds its promises over generated rows through the real layout', () => {
    for (let seed = 1; seed <= 300; seed += 1) {
      const random = seeded(seed);
      const specs: Spec[] = Array.from({ length: 2 + random(6) }, (_, index) => ({ id: `t${index}`, wanted: random(4) === 0 ? 50 + random(40) : undefined }));
      const tree = rightChain(specs);
      const ids = specs.map((spec) => (spec as { id: string }).id);
      const activeLeafId = ids[random(ids.length)];
      const input = { tree, activeLeafId, firstColumn: null, areaPx: 1000 + random(1600), narrowestPx: NARROWEST, pxPerCol: PX_PER_COL };
      const plan = resolveRowLayout(input);
      if (plan.screen.length < 2) continue;
      const divider = random(plan.screen.length - 1);
      const distance = random(1200) - 600;
      const result = applyColumnDrag(
        { screen: plan.screen, widths: plan.widths, firstColumn: plan.firstColumn },
        divider,
        distance,
        { activeLeafId, areaPx: input.areaPx, narrowestPx: NARROWEST, pxPerCol: PX_PER_COL },
      );
      const dragged = resolveRowLayout({ ...input, firstColumn: result.firstColumn, pendingWantedCols: result.wantedCols });
      expect(dragged.screen.flatMap((column) => column.leafIds)).toContain(activeLeafId);
      for (const width of dragged.widths) expect(width).toBeGreaterThanOrEqual(NARROWEST - 0.001);
      expect(dragged.widths.reduce((sum, width) => sum + width, 0)).toBeCloseTo(input.areaPx);
      for (const cols of result.wantedCols.values()) {
        expect(cols * PX_PER_COL).toBeGreaterThanOrEqual(NARROWEST - PX_PER_COL);
        expect(cols * PX_PER_COL).toBeLessThanOrEqual(input.areaPx);
      }
    }
  });
});

describe('planSlotSwap', () => {
  it.each<[string, string, string, ReturnType<typeof planSlotSwap>]>([
    ['chip right of the slot', 'd', 'b', { chipEdge: 'left', slotReturn: { anchorId: 'c', edge: 'right' } }],
    ['chip left of the slot', 'a', 'c', { chipEdge: 'right', slotReturn: { anchorId: 'b', edge: 'left' } }],
    ['chip next to the slot on the right', 'c', 'b', { chipEdge: 'left', slotReturn: null }],
    ['chip next to the slot on the left', 'b', 'c', { chipEdge: 'right', slotReturn: null }],
  ])('%s', (_name, chip, slot, swap) => {
    expect(planSlotSwap(rightChain(['a', 'b', 'c', 'd']), chip, slot)).toEqual(swap);
  });
});

describe('stepNarrowestCols', () => {
  const base = { tree: rightChain(ROW), activeLeafId: 'a', firstColumn: null, pxPerCol: PX_PER_COL };
  const cleared = (result: ReturnType<typeof stepNarrowestCols>) =>
    result.kind === 'clear-then-step' ? { cols: result.cols, leaves: result.columns.map((column) => column.leafIds[0]) } : result;

  it('clears only the dragged widths of the columns that end up on screen', () => {
    const incoming = rightChain(['a', { id: 'b', wanted: 140 }, 'c']);
    expect(cleared(stepNarrowestCols({ ...base, tree: incoming, areaPx: 1500 }, 66, 'more', 1500))).toEqual({ cols: 66, leaves: ['b'] });
    const farColumn = rightChain([{ id: 'a', wanted: 150 }, 'b', { id: 'c', wanted: 150 }]);
    expect(cleared(stepNarrowestCols({ ...base, tree: farColumn, areaPx: 1500 }, 66, 'more', 1500))).toEqual({ cols: 66, leaves: ['a'] });
  });

  it('keeps a saved width that has given way when clearing the one that blocks', () => {
    const tree = rightChain([{ id: 'a', wanted: 150 }, { id: 'b', wanted: 300 }, 'c']);
    expect(cleared(stepNarrowestCols({ ...base, tree, areaPx: 1500 }, 66, 'more', 1500))).toEqual({ cols: 66, leaves: ['a'] });
  });

  it('never reports a limit whose numbers fit the area', () => {
    const tree = rightChain([{ id: 'a', wanted: 100 }, 'b']);
    expect(stepNarrowestCols({ ...base, tree, areaPx: 1000 }, 66, 'more', 1000)).toEqual({
      kind: 'limit',
      message: '2 tiles at the narrowest width (57 cols, 570px) need 1140px. Tiles have 1000px here. A narrower width can be set in Settings → Appearance.',
    });
  });

  it('steps until one fewer or one more tile fits', () => {
    expect(stepNarrowestCols({ ...base, areaPx: 2100 }, 66, 'fewer', 2100)).toEqual({ kind: 'step', cols: 76 });
    expect(stepNarrowestCols({ ...base, areaPx: 2100 }, 76, 'more', 2100)).toEqual({ kind: 'step', cols: 66 });
    expect(stepNarrowestCols({ ...base, areaPx: 1200 }, 57, 'fewer', 1200)).toEqual({ kind: 'step', cols: 66 });
  });

  it('says the numbers when no step shows another tile', () => {
    expect(stepNarrowestCols({ ...base, areaPx: 1000 }, 57, 'more', 1100)).toEqual({
      kind: 'limit',
      message: '2 tiles at the narrowest width (57 cols, 570px) need 1140px. Tiles have 1000px here. A narrower width can be set in Settings → Appearance.',
    });
    expect(stepNarrowestCols({ ...base, areaPx: 1000 }, 57, 'more', 1240)).toEqual({
      kind: 'limit',
      message: '2 tiles at the narrowest width (57 cols, 570px) need 1140px. Tiles have 1000px here. Closing the sidebar would make room. A narrower width can be set in Settings → Appearance.',
    });
    expect(stepNarrowestCols({ ...base, areaPx: 500 }, 66, 'fewer', 500)).toEqual({ kind: 'limit', message: 'Already showing one tile.' });
    expect(stepNarrowestCols({ ...base, areaPx: 3100 }, 150, 'fewer', 3100)).toEqual({
      kind: 'limit',
      message: '2 tiles still fit at the widest width (150 cols, 1500px). Tiles have 3100px here.',
    });
    expect(stepNarrowestCols({ ...base, tree: rightChain(['a', 'b']), areaPx: 2000 }, 66, 'more', 2000)).toEqual({
      kind: 'limit',
      message: 'Every tile on this desktop is already on screen.',
    });
  });
});

describe('limit messages', () => {
  const base = { tree: rightChain(ROW), activeLeafId: 'a', firstColumn: null, pxPerCol: PX_PER_COL };
  const messageOf = (areaPx: number, cols: number, direction: 'fewer' | 'more') => {
    const result = stepNarrowestCols({ ...base, areaPx }, cols, direction, areaPx);
    return result.kind === 'limit' ? result.message : result;
  };

  it('points to Settings → Appearance when a narrower width than the step floor could fit another tile', () => {
    expect(messageOf(1700, 57, 'more')).toBe(
      '3 tiles at the narrowest width (57 cols, 570px) need 1710px. Tiles have 1700px here. A narrower width can be set in Settings → Appearance.',
    );
  });

  it('states the width in force when the setting is below the step floor', () => {
    expect(messageOf(1700, 52, 'more')).toBe('4 tiles at the narrowest width (52 cols, 520px) need 2080px. Tiles have 1700px here.');
    expect(messageOf(1700, 40, 'more')).toBe('4 tiles at the narrowest width (40 cols, 480px) need 1920px. Tiles have 1700px here.');
  });

  it('states the step floor, not the setting, when the setting is between steps', () => {
    expect(messageOf(1700, 70, 'more')).toBe(
      '3 tiles at the narrowest width (57 cols, 570px) need 1710px. Tiles have 1700px here. A narrower width can be set in Settings → Appearance.',
    );
  });

  it('states the width in force when the setting is above the widest step', () => {
    expect(messageOf(5000, 200, 'fewer')).toBe('2 tiles still fit at the widest width (200 cols, 2000px). Tiles have 5000px here.');
  });
});

describe('reachableTileCounts', () => {
  const base = { tree: rightChain(ROW), activeLeafId: 'a', firstColumn: null, pxPerCol: PX_PER_COL };

  it('lists only the tile counts some width can show', () => {
    expect(reachableTileCounts({ ...base, areaPx: 1451 }, 66)).toEqual([1, 2]);
    expect(reachableTileCounts({ ...base, areaPx: 2100 }, 66)).toEqual([1, 2, 3]);
  });

  it('keeps the current count when the setting is not a step', () => {
    expect(reachableTileCounts({ ...base, areaPx: 2100 }, 40)).toEqual([1, 2, 3, 4]);
    expect(reachableTileCounts({ ...base, areaPx: 1451 }, 70)).toEqual([1, 2]);
  });
});
