import {
  collectLayoutLeaves,
  leafSlotId,
  type TerminalLayoutNode,
  type TerminalSplitDirection,
} from '../../types/desktop';
import {
  ATTENTION_MIN_HEIGHT,
  ATTENTION_MIN_WIDTH,
  buildDividers,
  findLeaf,
  zoomLayoutTowardLeaf,
  type DesktopLayoutPlan,
  type DesktopLayoutView,
} from './attentionLayout';

export const NARROWEST_STEPS = [57, 66, 76, 86, 100, 120, 150] as const;
export const DEFAULT_NARROWEST_COLS = 66;

const FIT_TOLERANCE_PX = 0.5;
const NOTHING_SUSPENDED: ReadonlySet<string> = new Set();

export interface RowColumn {
  leafIds: string[];
  wantedCols?: number;
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
  areaPx: number;
  narrowestPx: number;
  pxPerCol: number;
  view?: DesktopLayoutView;
}

export interface RowLayoutPlan extends DesktopLayoutPlan {
  screen: RowColumn[];
  before: RowColumn[];
  after: RowColumn[];
  firstColumn: FirstColumn;
}

export function narrowestWidthPx(cols: number, pxPerCol: number): number {
  return Math.max(ATTENTION_MIN_WIDTH, cols * pxPerCol);
}

export function readRow(tree: TerminalLayoutNode): RowColumn[] {
  if (tree.type === 'split' && tree.direction === 'vertical') {
    return [...readRow(tree.children[0]), ...readRow(tree.children[1])];
  }
  const leaves = collectLayoutLeaves(tree);
  return [{ leafIds: leaves.map(leafSlotId), wantedCols: leaves[0]?.wantedCols, node: tree }];
}

export function columnIndexOf(row: readonly RowColumn[], leafId: string): number {
  return row.findIndex((column) => column.leafIds.includes(leafId));
}

function placeScreen(row: readonly RowColumn[], input: RowLayoutInput): { start: number; end: number } {
  const need = (index: number) =>
    Math.max(input.narrowestPx, (row[index].wantedCols ?? 0) * input.pxPerCol);
  const fits = (px: number) => px <= input.areaPx + FIT_TOLERANCE_PX;
  const fill = (start: number) => {
    let end = start + 1;
    let sum = need(start);
    while (end < row.length && fits(sum + need(end))) {
      sum += need(end);
      end += 1;
    }
    return { end, sum };
  };

  const activeIndex = columnIndexOf(row, input.activeLeafId);
  const remembered = input.firstColumn ? columnIndexOf(row, input.firstColumn.leafId) : 0;
  const from = remembered >= 0 ? remembered : input.firstColumn!.index;
  let start = Math.max(0, Math.min(from, activeIndex >= 0 ? activeIndex : row.length - 1));
  let filled = fill(start);
  while (filled.end <= activeIndex) {
    start += 1;
    filled = fill(start);
  }
  while (start > 0 && fits(filled.sum + need(start - 1))) {
    start -= 1;
    filled = fill(start);
  }
  return { start, end: filled.end };
}

function columnWidths(screen: readonly RowColumn[], input: RowLayoutInput): number[] {
  const wantedPx = (column: RowColumn) =>
    column.wantedCols ? Math.max(input.narrowestPx, column.wantedCols * input.pxPerCol) : null;
  const wantedSum = screen.reduce((sum, column) => sum + (wantedPx(column) ?? 0), 0);
  const shared = screen.filter((column) => !column.wantedCols).length;
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
  const row = readRow(input.tree);
  const { start, end } = placeScreen(row, input);
  const screen = row.slice(start, end);
  const placed = {
    screen,
    before: row.slice(0, start),
    after: row.slice(end),
    firstColumn: { leafId: screen[0].leafIds[0], index: start },
    suspendedLeafIds: NOTHING_SUSPENDED,
  };
  const view = input.view ?? { mode: 'normal' };
  if (view.mode === 'focused') {
    return { ...placed, renderedTree: findLeaf(input.tree, view.leafId) ?? input.tree, dividers: [] };
  }
  const renderedTree = screenTree(screen, columnWidths(screen, input));
  if (view.mode === 'zoomed') {
    return { ...placed, renderedTree: zoomLayoutTowardLeaf(renderedTree, view.leafId), dividers: [] };
  }
  const columnBoundaries = new Set(screen.map(screenSplitId));
  return {
    ...placed,
    renderedTree,
    dividers: buildDividers(renderedTree, NOTHING_SUSPENDED).filter(
      (divider) => !columnBoundaries.has(divider.splitId),
    ),
  };
}

export type NarrowestStep =
  | { kind: 'step'; cols: number }
  | { kind: 'limit'; message: string };

export function stepNarrowestCols(
  input: Omit<RowLayoutInput, 'narrowestPx' | 'view'>,
  currentCols: number,
  direction: 'fewer' | 'more',
  windowPx: number,
): NarrowestStep {
  const shownAt = (cols: number) =>
    resolveRowLayout({ ...input, narrowestPx: narrowestWidthPx(cols, input.pxPerCol) }).screen.length;
  const shown = shownAt(currentCols);
  const total = readRow(input.tree).length;
  const candidates = direction === 'fewer'
    ? NARROWEST_STEPS.filter((cols) => cols > currentCols)
    : NARROWEST_STEPS.filter((cols) => cols < currentCols).reverse();
  const cols = candidates.find((step) => (direction === 'fewer' ? shownAt(step) < shown : shownAt(step) > shown));
  if (cols !== undefined) return { kind: 'step', cols };
  const extreme = direction === 'fewer' ? NARROWEST_STEPS[NARROWEST_STEPS.length - 1] : NARROWEST_STEPS[0];
  const extremePx = narrowestWidthPx(extreme, input.pxPerCol);
  if (direction === 'fewer') {
    return {
      kind: 'limit',
      message: shown <= 1
        ? 'Already showing one tile.'
        : `${shown} tiles still fit at the widest step (${extreme} cols, ${extremePx}px). Tiles have ${input.areaPx}px here.`,
    };
  }
  if (shown >= total) return { kind: 'limit', message: 'Every tile on this desktop is already on screen.' };
  const asked = shown + 1;
  const askedPx = asked * extremePx;
  const sidebarWouldHelp = input.areaPx < windowPx && askedPx <= windowPx;
  return {
    kind: 'limit',
    message: `${asked} tiles at the narrowest step (${extreme} cols, ${extremePx}px) need ${askedPx}px. Tiles have ${input.areaPx}px here.${sidebarWouldHelp ? ' Closing the sidebar would make room.' : ''}`,
  };
}

export function rowDragRatioBounds(direction: TerminalSplitDirection, spanPx: number): { min: number; max: number } {
  if (spanPx <= 0) return { min: 0.1, max: 0.9 };
  const smallest = (direction === 'vertical' ? ATTENTION_MIN_WIDTH : ATTENTION_MIN_HEIGHT) / spanPx;
  return { min: Math.min(0.5, smallest), max: Math.max(0.5, 1 - smallest) };
}

export function lastUsedLeaf(column: RowColumn, focusOrder: readonly string[]): string {
  return focusOrder.find((leafId) => column.leafIds.includes(leafId)) ?? column.leafIds[0];
}
