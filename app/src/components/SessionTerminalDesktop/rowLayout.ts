import {
  collectLayoutLeaves,
  getNormalizedPaneBounds,
  leafSlotId,
  type SplitDivider,
  type TerminalLayoutNode,
  type TerminalSplitDirection,
} from '../../types/desktop';
import {
  TILE_MIN_HEIGHT,
  TILE_MIN_WIDTH,
  buildDividers,
  findLeaf,
  zoomLayoutTowardLeaf,
  type DesktopLayoutView,
} from './desktopLayout';

export const NARROWEST_STEPS = [57, 66, 76, 86, 100, 120, 150] as const;
export const DEFAULT_NARROWEST_COLS = 66;

const FIT_TOLERANCE_PX = 0.5;
const COLUMN_WIDTH_UNCHANGED_PX = 0.25;

export interface RowColumn {
  leafIds: string[];
  wantedCols?: number;
  wantedAreaCols?: number;
  node: TerminalLayoutNode;
}

export interface FirstColumn {
  leafId: string;
  index: number;
}

export interface RowLayoutInput {
  tree: TerminalLayoutNode;
  activeLeafId: string;
  firstColumn: FirstColumn | null;
  heldLeafIds?: readonly string[];
  areaPx: number;
  narrowestPx: number;
  pxPerCol: number;
  pendingWantedCols?: ReadonlyMap<string, number>;
  view?: DesktopLayoutView;
}

export interface RowLayoutPlan {
  renderedTree: TerminalLayoutNode;
  dividers: SplitDivider[];
  screen: RowColumn[];
  before: RowColumn[];
  after: RowColumn[];
  firstColumn: FirstColumn;
  widths: number[];
}

export function narrowestWidthPx(cols: number, pxPerCol: number): number {
  return Math.max(TILE_MIN_WIDTH, cols * pxPerCol);
}

export function readRow(tree: TerminalLayoutNode): RowColumn[] {
  if (tree.type === 'split' && tree.direction === 'vertical') {
    return [...readRow(tree.children[0]), ...readRow(tree.children[1])];
  }
  const leaves = collectLayoutLeaves(tree);
  return [{ leafIds: leaves.map(leafSlotId), wantedCols: leaves[0]?.wantedCols, wantedAreaCols: leaves[0]?.wantedAreaCols, node: tree }];
}

function withPendingWantedCols(row: readonly RowColumn[], pending: ReadonlyMap<string, number> | undefined): RowColumn[] {
  if (!pending?.size) return [...row];
  return row.map((column) => {
    const cols = pending.get(column.leafIds[0]);
    return cols === undefined ? column : { ...column, wantedCols: cols || undefined, wantedAreaCols: undefined };
  });
}

export function columnIndexOf(row: readonly RowColumn[], leafId: string): number {
  return row.findIndex((column) => column.leafIds.includes(leafId));
}

export function shownRow(row: readonly RowColumn[], heldLeafIds: readonly string[] | undefined, activeLeafId: string): RowColumn[] {
  if (!heldLeafIds?.length) return [...row];
  return row.filter((column) => column.leafIds.includes(activeLeafId) || !column.leafIds.some((id) => heldLeafIds.includes(id)));
}

type RowMeasures = Pick<RowLayoutInput, 'areaPx' | 'narrowestPx' | 'pxPerCol'>;

function fitsArea(px: number, measures: Pick<RowMeasures, 'areaPx'>): boolean {
  return px <= measures.areaPx + FIT_TOLERANCE_PX;
}

export function areaInCols({ areaPx, pxPerCol }: Pick<RowMeasures, 'areaPx' | 'pxPerCol'>): number {
  return Math.floor(areaPx / pxPerCol);
}

function wantedPxWithRoom(column: RowColumn, measures: Pick<RowMeasures, 'areaPx' | 'pxPerCol'>): number | null {
  const px = (column.wantedCols ?? 0) * measures.pxPerCol;
  const areaHoldsIt = areaInCols(measures) >= (column.wantedAreaCols ?? 0);
  return px > 0 && areaHoldsIt && fitsArea(px, measures) ? px : null;
}

function columnNeedPx(column: RowColumn, measures: RowMeasures): number {
  return Math.max(measures.narrowestPx, wantedPxWithRoom(column, measures) ?? 0);
}

function fillFrom(row: readonly RowColumn[], start: number, measures: RowMeasures): { end: number; sum: number } {
  let end = start + 1;
  let sum = columnNeedPx(row[start], measures);
  while (end < row.length && fitsArea(sum + columnNeedPx(row[end], measures), measures)) {
    sum += columnNeedPx(row[end], measures);
    end += 1;
  }
  return { end, sum };
}

function placeScreen(
  row: readonly RowColumn[],
  input: RowMeasures & Pick<RowLayoutInput, 'activeLeafId' | 'firstColumn'>,
): { start: number; end: number; sum: number } {
  const activeIndex = columnIndexOf(row, input.activeLeafId);
  const remembered = input.firstColumn ? columnIndexOf(row, input.firstColumn.leafId) : 0;
  const from = remembered >= 0 ? remembered : input.firstColumn!.index;
  let start = Math.max(0, Math.min(from, activeIndex >= 0 ? activeIndex : row.length - 1));
  let filled = fillFrom(row, start, input);
  while (filled.end <= activeIndex) {
    start += 1;
    filled = fillFrom(row, start, input);
  }
  while (start > 0 && fitsArea(filled.sum + columnNeedPx(row[start - 1], input), input)) {
    start -= 1;
    filled = fillFrom(row, start, input);
  }
  return { start, ...filled };
}

function columnWidths(screen: readonly RowColumn[], input: RowLayoutInput): number[] {
  const wantedPx = (column: RowColumn) => (wantedPxWithRoom(column, input) === null ? null : columnNeedPx(column, input));
  const wantedSum = screen.reduce((sum, column) => sum + (wantedPx(column) ?? 0), 0);
  const shared = screen.filter((column) => wantedPx(column) === null).length;
  if (shared === 0) {
    return screen.map((column) => (wantedPx(column)! * input.areaPx) / wantedSum);
  }
  const each = (input.areaPx - wantedSum) / shared;
  return screen.map((column) => wantedPx(column) ?? each);
}

function screenSplitId(column: RowColumn): string {
  return `row:${column.leafIds[0]}`;
}

function screenTree(screen: readonly RowColumn[], widths: readonly number[]): TerminalLayoutNode {
  let tree = screen[screen.length - 1].node;
  let span = widths[widths.length - 1];
  for (let index = screen.length - 2; index >= 0; index -= 1) {
    span += widths[index];
    tree = {
      type: 'split',
      splitId: screenSplitId(screen[index]),
      direction: 'vertical',
      ratio: span > 0 ? widths[index] / span : 1 / (screen.length - index),
      ratioMode: 'preferred',
      children: [screen[index].node, tree],
    };
  }
  return tree;
}

export function resolveRowLayout(input: RowLayoutInput): RowLayoutPlan {
  const row = withPendingWantedCols(readRow(input.tree), input.pendingWantedCols);
  const shown = shownRow(row, input.heldLeafIds, input.activeLeafId);
  const { start, end } = placeScreen(shown, input);
  const screen = shown.slice(start, end);
  const firstAt = row.indexOf(screen[0]);
  const widths = columnWidths(screen, input);
  const placed = {
    screen,
    before: row.slice(0, firstAt),
    after: row.slice(firstAt).filter((column) => !screen.includes(column)),
    firstColumn: { leafId: screen[0].leafIds[0], index: start },
    widths,
  };
  const view = input.view ?? { mode: 'normal' };
  if (view.mode === 'focused') {
    return { ...placed, renderedTree: findLeaf(input.tree, view.leafId) ?? input.tree, dividers: [] };
  }
  const renderedTree = screenTree(screen, widths);
  if (view.mode === 'zoomed') {
    return { ...placed, renderedTree: zoomLayoutTowardLeaf(renderedTree, view.leafId), dividers: [] };
  }
  return { ...placed, renderedTree, dividers: buildDividers(renderedTree) };
}

export function columnDividerIndex(screen: readonly RowColumn[], splitId: string): number {
  return screen.slice(0, -1).findIndex((column) => screenSplitId(column) === splitId);
}

export interface ColumnDragStart {
  screen: readonly RowColumn[];
  widths: readonly number[];
  firstColumn: FirstColumn;
}

export interface ColumnDragMeasures {
  activeLeafId: string;
  areaPx: number;
  narrowestPx: number;
  pxPerCol: number;
}

export interface ColumnDrag {
  wantedCols: ReadonlyMap<string, number>;
  firstColumn: FirstColumn;
  stopped: boolean;
}

export function applyColumnDrag(
  start: ColumnDragStart,
  dividerIndex: number,
  distancePx: number,
  { activeLeafId, areaPx, narrowestPx, pxPerCol }: ColumnDragMeasures,
): ColumnDrag {
  const widths = [...start.widths];
  const growsLeft = distancePx > 0;
  const grower = growsLeft ? dividerIndex : dividerIndex + 1;
  const left = start.screen.map((_, index) => index).slice(0, dividerIndex + 1).reverse();
  const right = start.screen.map((_, index) => index).slice(dividerIndex + 1);
  let givers = growsLeft ? right : left;
  let amount = Math.abs(distancePx);
  let stopped = false;
  let leftCount = 0;

  for (;;) {
    const spare = givers.reduce((sum, index) => sum + Math.max(0, widths[index] - narrowestPx), 0);
    if (amount <= spare) break;
    const far = givers[givers.length - 1];
    if (start.screen[far].leafIds.includes(activeLeafId)) {
      amount = spare;
      stopped = true;
      break;
    }
    const farWidth = widths[far];
    givers = givers.slice(0, -1);
    leftCount += 1;
    if (givers.length === 0) {
      amount = farWidth;
      stopped = true;
      break;
    }
    widths[givers[0]] += farWidth;
  }

  let owed = amount;
  for (const index of givers) {
    const taken = Math.min(owed, Math.max(0, widths[index] - narrowestPx));
    widths[index] -= taken;
    owed -= taken;
  }
  widths[grower] += amount;

  const stays = new Set([grower, ...givers, ...(growsLeft ? left : right)]);
  const moved = (index: number) => Math.abs(widths[index] - start.widths[index]) > COLUMN_WIDTH_UNCHANGED_PX;
  const shownAtOtherWidth = (column: RowColumn, index: number) =>
    wantedPxWithRoom(column, { areaPx, pxPerCol }) !== null
    && Math.abs(start.widths[index] - columnNeedPx(column, { areaPx, narrowestPx, pxPerCol })) > COLUMN_WIDTH_UNCHANGED_PX;
  const wantedCols = new Map<string, number>();
  if (start.screen.some((_, index) => moved(index))) {
    let savedPx = 0;
    start.screen.forEach((column, index) => {
      if (stays.has(index) && (moved(index) || shownAtOtherWidth(column, index))) {
        const colsBefore = Math.floor(savedPx / pxPerCol);
        savedPx += widths[index];
        wantedCols.set(column.leafIds[0], Math.floor(savedPx / pxPerCol) - colsBefore);
      }
    });
  }
  const firstColumn = growsLeft || leftCount === 0
    ? start.firstColumn
    : { leafId: start.screen[leftCount].leafIds[0], index: start.firstColumn.index + leftCount };
  return { wantedCols, firstColumn, stopped };
}

export interface SlotSwap {
  chipEdge: 'left' | 'right';
  slotReturn: { anchorId: string; edge: 'left' | 'right' } | null;
}

export function planSlotSwap(tree: TerminalLayoutNode, chipLeafId: string, slotLeafId: string): SlotSwap {
  const row = readRow(tree);
  const chipAt = columnIndexOf(row, chipLeafId);
  const slotAt = columnIndexOf(row, slotLeafId);
  const fromLeft = chipAt < slotAt;
  const neighbourAt = chipAt + (fromLeft ? 1 : -1);
  return {
    chipEdge: fromLeft ? 'right' : 'left',
    slotReturn: neighbourAt === slotAt ? null : { anchorId: row[neighbourAt].leafIds[0], edge: fromLeft ? 'left' : 'right' },
  };
}

export type NarrowestStep =
  | { kind: 'step'; cols: number }
  | { kind: 'clear-then-step'; cols: number; columns: RowColumn[]; shown: number }
  | { kind: 'limit'; message: string };

type StepInput = Omit<RowLayoutInput, 'narrowestPx' | 'view'>;

function tilesShownAt(input: StepInput, cols: number): number {
  return resolveRowLayout({ ...input, narrowestPx: narrowestWidthPx(cols, input.pxPerCol) }).screen.length;
}

export function reachableTileCounts(input: StepInput, currentCols: number): number[] {
  const counts = new Set([currentCols, ...NARROWEST_STEPS].map((cols) => tilesShownAt(input, cols)));
  const more = stepNarrowestCols(input, currentCols, 'more', 0);
  if (more.kind === 'clear-then-step') counts.add(more.shown);
  return [...counts].sort((a, b) => a - b);
}

export function stepNarrowestCols(
  input: StepInput,
  currentCols: number,
  direction: 'fewer' | 'more',
  windowPx: number,
): NarrowestStep {
  const shownAt = (cols: number) => tilesShownAt(input, cols);
  const shown = shownAt(currentCols);
  const total = shownRow(readRow(input.tree), input.heldLeafIds, input.activeLeafId).length;
  const candidates = direction === 'fewer'
    ? NARROWEST_STEPS.filter((cols) => cols > currentCols)
    : NARROWEST_STEPS.filter((cols) => cols < currentCols).reverse();
  const cols = candidates.find((step) => (direction === 'fewer' ? shownAt(step) < shown : shownAt(step) > shown));
  if (cols !== undefined) return { kind: 'step', cols };
  if (direction === 'more') {
    const row = withPendingWantedCols(readRow(input.tree), input.pendingWantedCols);
    const noWantedWidths = new Map(row.map((column) => [column.leafIds[0], 0]));
    const screenWithout = (step: number) =>
      resolveRowLayout({ ...input, pendingWantedCols: noWantedWidths, narrowestPx: narrowestWidthPx(step, input.pxPerCol) }).screen;
    const unblocked = [currentCols, ...candidates].find((step) => screenWithout(step).length > shown);
    if (unblocked !== undefined) {
      const screenLeaves = new Set(screenWithout(unblocked).map((column) => column.leafIds[0]));
      return {
        kind: 'clear-then-step',
        cols: unblocked,
        shown: screenLeaves.size,
        columns: row.filter((column) => wantedPxWithRoom(column, input) !== null && screenLeaves.has(column.leafIds[0])),
      };
    }
  }
  if (direction === 'fewer') {
    const widest = Math.max(NARROWEST_STEPS[NARROWEST_STEPS.length - 1], currentCols);
    return {
      kind: 'limit',
      message: shown <= 1
        ? 'Already showing one tile.'
        : `${shown} tiles still fit at the widest width (${widest} cols, ${narrowestWidthPx(widest, input.pxPerCol)}px). Tiles have ${input.areaPx}px here.`,
    };
  }
  if (shown >= total) return { kind: 'limit', message: 'Every tile on this desktop is already on screen.' };
  const narrowest = Math.min(NARROWEST_STEPS[0], currentCols);
  const narrowestPx = narrowestWidthPx(narrowest, input.pxPerCol);
  const asked = shown + 1;
  const askedPx = asked * narrowestPx;
  const sidebarWouldHelp = input.areaPx < windowPx && askedPx <= windowPx;
  const narrowerWouldHelp = narrowestPx > TILE_MIN_WIDTH && asked * TILE_MIN_WIDTH <= input.areaPx;
  return {
    kind: 'limit',
    message: `${asked} tiles at the narrowest width (${narrowest} cols, ${narrowestPx}px) need ${askedPx}px. Tiles have ${input.areaPx}px here.${sidebarWouldHelp ? ' Closing the sidebar would make room.' : ''}${narrowerWouldHelp ? ' A narrower width can be set in Settings → Appearance.' : ''}`,
  };
}

export function rowDragRatioBounds(direction: TerminalSplitDirection, spanPx: number): { min: number; max: number } {
  if (spanPx <= 0) return { min: 0.1, max: 0.9 };
  const smallest = (direction === 'vertical' ? TILE_MIN_WIDTH : TILE_MIN_HEIGHT) / spanPx;
  return { min: Math.min(0.5, smallest), max: Math.max(0.5, 1 - smallest) };
}

export function lastUsedLeaf(column: RowColumn, focusOrder: readonly string[]): string {
  return focusOrder.find((leafId) => column.leafIds.includes(leafId)) ?? column.leafIds[0];
}

export type ShelfSend =
  | { kind: 'alone' }
  | { kind: 'unstack'; leafId: string; anchorId: string; nextLeafId: string }
  | { kind: 'hold'; leafId: string; nextLeafId: string }
  | { kind: 'move'; leafId: string; anchorId: string; edge: 'left' | 'right'; nextLeafId: string };

export function planSendToShelf(
  input: Omit<RowLayoutInput, 'view'>,
  screen: readonly RowColumn[],
  focusOrder: readonly string[],
): ShelfSend {
  const leafId = input.activeLeafId;
  const row = readRow(input.tree);
  const column = row[columnIndexOf(row, leafId)];
  if (column.leafIds.length > 1) {
    const rest = column.leafIds.filter((id) => id !== leafId);
    const lastColumn = row[row.length - 1].leafIds.filter((id) => id !== leafId);
    return {
      kind: 'unstack',
      leafId,
      anchorId: lastColumn[lastColumn.length - 1],
      nextLeafId: rest[Math.min(column.leafIds.indexOf(leafId), rest.length - 1)],
    };
  }

  const shown = shownRow(row, input.heldLeafIds, leafId);
  const shownAt = shown.indexOf(column);
  const slot = columnIndexOf(screen, leafId);
  const inSlot = screen[slot + 1] ?? screen[slot - 1] ?? shown[shownAt + 1] ?? shown[shownAt - 1];
  if (!inSlot) return { kind: 'alone' };

  const rest = shown.filter((candidate) => candidate !== column);
  const placed = placeScreen(rest, { ...input, activeLeafId: inSlot.leafIds[0] });
  const nextColumn = rest[Math.min(placed.start + slot, placed.end - 1)];
  const nextLeafId = lastUsedLeaf(nextColumn, focusOrder);
  if (fitsArea(placed.sum + columnNeedPx(column, input), input)) return { kind: 'hold', leafId, nextLeafId };

  const others = row.filter((candidate) => candidate !== column);
  const restRight = screen[screen.length - 1].leafIds[0] !== shown[shown.length - 1].leafIds[0];
  return restRight
    ? { kind: 'move', leafId, anchorId: others[others.length - 1].leafIds[0], edge: 'right', nextLeafId }
    : { kind: 'move', leafId, anchorId: others[0].leafIds[0], edge: 'left', nextLeafId };
}

export function revealFirstColumn(input: Omit<RowLayoutInput, 'view'>, leafId: string, currentStart: number): FirstColumn | null {
  const shown = shownRow(readRow(input.tree), input.heldLeafIds, input.activeLeafId);
  const leafAt = columnIndexOf(shown, leafId);
  const activeAt = columnIndexOf(shown, input.activeLeafId);
  if (leafAt < 0 || activeAt < 0) return null;
  let best: number | null = null;
  for (let start = 0; start <= Math.min(leafAt, activeAt); start += 1) {
    const { end } = fillFrom(shown, start, input);
    const holdsBoth = leafAt < end && activeAt < end;
    if (holdsBoth && (best === null || Math.abs(start - currentStart) < Math.abs(best - currentStart))) best = start;
  }
  return best === null ? null : { leafId: shown[best].leafIds[0], index: best };
}

export function firstColumnStillShows(input: Omit<RowLayoutInput, 'view'>, firstColumn: FirstColumn): boolean {
  const shown = shownRow(readRow(input.tree), input.heldLeafIds, input.activeLeafId);
  const start = columnIndexOf(shown, firstColumn.leafId);
  const activeAt = columnIndexOf(shown, input.activeLeafId);
  return start >= 0 && activeAt >= start && activeAt < fillFrom(shown, start, input).end;
}

export function columnStackNeedPx(tree: TerminalLayoutNode, leafId: string): number {
  const row = readRow(tree);
  const column = row[columnIndexOf(row, leafId)];
  if (!column) return TILE_MIN_HEIGHT;
  const heights = [...getNormalizedPaneBounds(column.node).values()].map((bounds) => bounds.height);
  return TILE_MIN_HEIGHT / Math.min(...heights);
}

export function stackLimitMessage(tree: TerminalLayoutNode, anchorLeafId: string, tileHeightPx: number): string | null {
  const row = readRow(tree);
  const column = row[columnIndexOf(row, anchorLeafId)];
  if (!column) return null;
  const asked = column.leafIds.length + 1;
  const neededPx = asked * TILE_MIN_HEIGHT;
  if (neededPx <= tileHeightPx) return null;
  return `${asked} stacked tiles at the ${TILE_MIN_HEIGHT}px minimum need ${neededPx}px. Tiles have ${tileHeightPx}px here, so it opened beside instead.`;
}
