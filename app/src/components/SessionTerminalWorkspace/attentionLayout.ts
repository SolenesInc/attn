import {
  applyRatioOverrides,
  collectLayoutLeaves,
  getNormalizedPaneBounds,
  hasLeaf,
  leafSlotId,
  type SplitDivider,
  type TerminalLayoutNode,
} from '../../types/workspace';

export const ATTENTION_MIN_WIDTH = 480;
export const ATTENTION_MIN_HEIGHT = 320;
export const ATTENTION_SLIVER_SIZE = 34;
const ZOOM_PATH_RATIO = 0.76;

export interface AttentionViewport {
  width: number;
  height: number;
}

export type WorkspaceLayoutView =
  | { mode: 'normal' }
  | { mode: 'focused'; leafId: string }
  | { mode: 'zoomed'; leafId: string };

export interface ResolveWorkspaceLayoutInput {
  sourceTree: TerminalLayoutNode;
  viewport: AttentionViewport;
  activeLeafId: string;
  focusOrder: readonly string[];
  previousSuspendedLeafIds: ReadonlySet<string>;
// Leaves the user folded by dragging: they stay folded through restore
// passes until a click, a drag freeing room, or removal releases them.
  pinnedLeafIds?: ReadonlySet<string>;
// While a divider drag is live, restores wait for the drag to end; a fold
// freeing room mid-drag must not pop another leaf open under the pointer.
  holdRestores?: boolean;
  pendingRatioOverrides?: ReadonlyMap<string, number>;
  view?: WorkspaceLayoutView;
}

export interface WorkspaceLayoutPlan {
  renderedTree: TerminalLayoutNode;
  suspendedLeafIds: ReadonlySet<string>;
  dividers: SplitDivider[];
}

function isDocument(node: TerminalLayoutNode): boolean {
  return node.type === 'tile' && (node.tileKind === 'markdown' || node.tileKind === 'seed');
}

function subtreeMass(node: TerminalLayoutNode, suspended: ReadonlySet<string>): number {
  if (node.type !== 'split') {
    if (suspended.has(leafSlotId(node))) {
      return 0;
    }
    return isDocument(node) ? 1.5 : 1;
  }
  return subtreeMass(node.children[0], suspended) + subtreeMass(node.children[1], suspended);
}

function subtreeHasDocument(node: TerminalLayoutNode): boolean {
  if (node.type !== 'split') {
    return isDocument(node);
  }
  return subtreeHasDocument(node.children[0]) || subtreeHasDocument(node.children[1]);
}

function preferredRatio(
  node: Extract<TerminalLayoutNode, { type: 'split' }>,
  suspended: ReadonlySet<string>,
  pendingPreferredSplitIds: ReadonlySet<string>,
): number {
  const ratio = node.ratio > 0 && node.ratio < 1 ? node.ratio : 0.5;
  if (
    node.ratioMode === 'preferred'
    || pendingPreferredSplitIds.has(node.splitId)
    || !subtreeHasDocument(node)
  ) {
    return ratio;
  }
  const first = subtreeMass(node.children[0], suspended);
  const second = subtreeMass(node.children[1], suspended);
  if (first + second === 0) {
    return ratio;
  }
  return first / (first + second);
}

interface MinimumSize {
  width: number;
  height: number;
}

function minimumSize(node: TerminalLayoutNode, suspended: ReadonlySet<string>): MinimumSize {
  if (node.type !== 'split') {
    return suspended.has(leafSlotId(node))
      ? { width: ATTENTION_SLIVER_SIZE, height: ATTENTION_SLIVER_SIZE }
      : { width: ATTENTION_MIN_WIDTH, height: ATTENTION_MIN_HEIGHT };
  }
  const first = minimumSize(node.children[0], suspended);
  const second = minimumSize(node.children[1], suspended);
  return node.direction === 'vertical'
    ? { width: first.width + second.width, height: Math.max(first.height, second.height) }
    : { width: Math.max(first.width, second.width), height: first.height + second.height };
}

function allLeavesSuspended(node: TerminalLayoutNode, suspended: ReadonlySet<string>): boolean {
  return collectLayoutLeaves(node).every((leaf) => suspended.has(leafSlotId(leaf)));
}

function clampRatioToMinimums(
  ratio: number,
  span: number,
  firstMinimum: number,
  secondMinimum: number,
  firstFullySuspended: boolean,
  secondFullySuspended: boolean,
): number {
  const minimumTotal = firstMinimum + secondMinimum;
  if (span <= 0) {
    return ratio;
  }
  if (firstFullySuspended !== secondFullySuspended) {
    if (firstFullySuspended && firstMinimum < span) {
      return firstMinimum / span;
    }
    if (secondFullySuspended && secondMinimum < span) {
      return 1 - (secondMinimum / span);
    }
  }
  if (minimumTotal > span) {
    return firstMinimum / minimumTotal;
  }
  return Math.min(1 - (secondMinimum / span), Math.max(firstMinimum / span, ratio));
}

function projectNode(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
  pendingPreferredSplitIds: ReadonlySet<string>,
  width: number,
  height: number,
): TerminalLayoutNode {
  if (node.type !== 'split') {
    return node;
  }

  const span = node.direction === 'vertical' ? width : height;
  const firstMinimum = minimumSize(node.children[0], suspended);
  const secondMinimum = minimumSize(node.children[1], suspended);
  const ratio = clampRatioToMinimums(
    preferredRatio(node, suspended, pendingPreferredSplitIds),
    span,
    node.direction === 'vertical' ? firstMinimum.width : firstMinimum.height,
    node.direction === 'vertical' ? secondMinimum.width : secondMinimum.height,
    allLeavesSuspended(node.children[0], suspended),
    allLeavesSuspended(node.children[1], suspended),
  );

  const firstWidth = node.direction === 'vertical' ? width * ratio : width;
  const secondWidth = node.direction === 'vertical' ? width * (1 - ratio) : width;
  const firstHeight = node.direction === 'horizontal' ? height * ratio : height;
  const secondHeight = node.direction === 'horizontal' ? height * (1 - ratio) : height;
  return {
    ...node,
    ratio,
    children: [
      projectNode(node.children[0], suspended, pendingPreferredSplitIds, firstWidth, firstHeight),
      projectNode(node.children[1], suspended, pendingPreferredSplitIds, secondWidth, secondHeight),
    ],
  };
}

function projectLayout(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
  viewport: AttentionViewport,
  pendingPreferredSplitIds: ReadonlySet<string>,
): TerminalLayoutNode {
  return projectNode(node, suspended, pendingPreferredSplitIds, viewport.width, viewport.height);
}

function visibleLeavesFit(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
  viewport: AttentionViewport,
  pendingPreferredSplitIds: ReadonlySet<string>,
  gutter = 0,
): boolean {
  const projected = projectLayout(node, suspended, viewport, pendingPreferredSplitIds);
  const bounds = getNormalizedPaneBounds(projected);
  for (const [leafId, frame] of bounds) {
    if (suspended.has(leafId)) {
      continue;
    }
    if (
      frame.width * viewport.width < ATTENTION_MIN_WIDTH + gutter - 0.5
      || frame.height * viewport.height < ATTENTION_MIN_HEIGHT + gutter - 0.5
    ) {
      return false;
    }
  }
  return true;
}

function sameIds(first: ReadonlySet<string>, second: ReadonlySet<string>): boolean {
  return first.size === second.size && [...first].every((id) => second.has(id));
}

// Size rank comes from user-set ratios alone (automatic splits stay
// neutral), so untouched layouts tie exactly and fall back to staleness.
function intentSizeScores(
  node: TerminalLayoutNode,
  pendingPreferredSplitIds: ReadonlySet<string>,
): Map<string, number> {
  const scores = new Map<string, number>();
  const walk = (current: TerminalLayoutNode, score: number): void => {
    if (current.type !== 'split') {
      scores.set(leafSlotId(current), score);
      return;
    }
    const intentional = current.ratioMode === 'preferred'
      || pendingPreferredSplitIds.has(current.splitId);
    const ratio = intentional && current.ratio > 0 && current.ratio < 1 ? current.ratio : 0.5;
    walk(current.children[0], intentional ? score * ratio : score);
    walk(current.children[1], intentional ? score * (1 - ratio) : score);
  };
  walk(node, 1);
  return scores;
}

// Staleness breaks size ties: never-focused leaves fold before anything in
// the focus order, and the most recently focused leaf folds last.
function stalenessRank(leafIds: readonly string[], focusOrder: readonly string[]): Map<string, number> {
  const focusedIds = new Set(focusOrder);
  return new Map([
    ...[...leafIds].reverse().filter((id) => !focusedIds.has(id)),
    ...[...focusOrder].reverse(),
  ].filter((id, index, all) => all.indexOf(id) === index).map((id, index) => [id, index]));
}

function smallestLeaf(
  candidates: readonly string[],
  scores: Map<string, number>,
  staleness: Map<string, number>,
): string | undefined {
  const score = (id: string): number => scores.get(id) ?? 1;
  return [...candidates].sort((a, b) => (
    (score(a) - score(b)) || ((staleness.get(a) ?? 0) - (staleness.get(b) ?? 0))
  ))[0];
}

function reconcileSuspension(
  node: TerminalLayoutNode,
  current: ReadonlySet<string>,
  viewport: AttentionViewport,
  protectedLeafId: string,
  focusOrder: readonly string[],
  pendingPreferredSplitIds: ReadonlySet<string>,
  pinned: ReadonlySet<string> = new Set(),
  holdRestores = false,
): ReadonlySet<string> {
  const leafIds = collectLayoutLeaves(node).map(leafSlotId);
  const valid = new Set(leafIds);
  const next = new Set(
    [...current, ...pinned].filter((id) => valid.has(id) && id !== protectedLeafId),
  );
  if (viewport.width <= 0 || viewport.height <= 0) {
    return sameIds(current, next) ? current : next;
  }

  const staleness = stalenessRank(leafIds, focusOrder);
  const scores = intentSizeScores(node, pendingPreferredSplitIds);

  while (!visibleLeavesFit(node, next, viewport, pendingPreferredSplitIds)) {
    const visibleCount = leafIds.filter((id) => !next.has(id)).length;
    if (visibleCount <= 1) {
      break;
    }
    const victim = smallestLeaf(
      leafIds.filter((id) => id !== protectedLeafId && !next.has(id)),
      scores,
      staleness,
    );
    if (!victim) {
      break;
    }
    next.add(victim);
  }

  for (const candidate of holdRestores ? [] : [...next].reverse()) {
    if (pinned.has(candidate)) {
      continue;
    }
    const restored = new Set(next);
    restored.delete(candidate);
    if (visibleLeavesFit(node, restored, viewport, pendingPreferredSplitIds)) {
      next.delete(candidate);
    }
  }

  return sameIds(current, next) ? current : next;
}

interface DividerAncestor {
  node: Extract<TerminalLayoutNode, { type: 'split' }>;
  left: number;
  top: number;
  right: number;
  bottom: number;
  ratio: number;
  childIndex: 0 | 1;
}

// A boundary beside a sliver re-aims (grabRatio) at the nearest same-axis
// split separating its visible sides; only viewport edge beyond means no divider.
function buildDividers(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
): SplitDivider[] {
  const dividers: SplitDivider[] = [];
  const walk = (
    current: TerminalLayoutNode,
    left: number,
    top: number,
    right: number,
    bottom: number,
    ancestors: readonly DividerAncestor[],
  ): void => {
    if (current.type !== 'split') {
      return;
    }
    const ratio = current.ratio > 0 && current.ratio < 1 ? current.ratio : 0.5;
    const firstSuspended = allLeavesSuspended(current.children[0], suspended);
    const secondSuspended = allLeavesSuspended(current.children[1], suspended);
    if (!firstSuspended && !secondSuspended) {
      dividers.push({ splitId: current.splitId, direction: current.direction, ratio, left, top, right, bottom });
    } else if (firstSuspended !== secondSuspended) {
      const descentIndex = firstSuspended ? 1 : 0;
      const target = [...ancestors].reverse().find((ancestor) => (
        ancestor.node.direction === current.direction
        && ancestor.childIndex === descentIndex
        && !allLeavesSuspended(ancestor.node.children[1 - descentIndex], suspended)
      ));
      if (target) {
        const grabAbs = current.direction === 'vertical'
          ? left + (right - left) * ratio
          : top + (bottom - top) * ratio;
        const start = current.direction === 'vertical' ? target.left : target.top;
        const span = current.direction === 'vertical'
          ? target.right - target.left
          : target.bottom - target.top;
        if (span > 0) {
          dividers.push({
            splitId: target.node.splitId,
            direction: current.direction,
            ratio: target.ratio,
            left: target.left,
            top: target.top,
            right: target.right,
            bottom: target.bottom,
            grabRatio: (grabAbs - start) / span,
          });
        }
      }
    }
    const splitX = left + (right - left) * ratio;
    const splitY = top + (bottom - top) * ratio;
    const self = (childIndex: 0 | 1): DividerAncestor => (
      { node: current, left, top, right, bottom, ratio, childIndex }
    );
    if (current.direction === 'vertical') {
      walk(current.children[0], left, top, splitX, bottom, [...ancestors, self(0)]);
      walk(current.children[1], splitX, top, right, bottom, [...ancestors, self(1)]);
    } else {
      walk(current.children[0], left, top, right, splitY, [...ancestors, self(0)]);
      walk(current.children[1], left, splitY, right, bottom, [...ancestors, self(1)]);
    }
  };
  walk(node, 0, 0, 1, 1, []);
  return dividers;
}

function findLeaf(node: TerminalLayoutNode, leafId: string): TerminalLayoutNode | null {
  if (node.type !== 'split') {
    return leafSlotId(node) === leafId ? node : null;
  }
  return findLeaf(node.children[0], leafId) || findLeaf(node.children[1], leafId);
}

function zoomLayoutTowardLeaf(node: TerminalLayoutNode, leafId: string): TerminalLayoutNode {
  if (node.type !== 'split') {
    return node;
  }

  const firstContainsLeaf = hasLeaf(node.children[0], leafId);
  const secondContainsLeaf = hasLeaf(node.children[1], leafId);
  const children: [TerminalLayoutNode, TerminalLayoutNode] = [
    zoomLayoutTowardLeaf(node.children[0], leafId),
    zoomLayoutTowardLeaf(node.children[1], leafId),
  ];
  if (!firstContainsLeaf && !secondContainsLeaf) {
    return { ...node, children };
  }
  return {
    ...node,
    ratio: firstContainsLeaf ? ZOOM_PATH_RATIO : 1 - ZOOM_PATH_RATIO,
    children,
  };
}

export function resolveWorkspaceLayout(input: ResolveWorkspaceLayoutInput): WorkspaceLayoutPlan {
  const pendingRatioOverrides = input.pendingRatioOverrides ?? new Map<string, number>();
  const sourceTree = applyRatioOverrides(input.sourceTree, pendingRatioOverrides);
  const pendingPreferredSplitIds = new Set(pendingRatioOverrides.keys());
  const view = input.view ?? { mode: 'normal' };

  if (view.mode === 'focused') {
    return {
      renderedTree: findLeaf(sourceTree, view.leafId) ?? sourceTree,
      suspendedLeafIds: input.previousSuspendedLeafIds,
      dividers: [],
    };
  }
  if (view.mode === 'zoomed') {
    return {
      renderedTree: zoomLayoutTowardLeaf(sourceTree, view.leafId),
      suspendedLeafIds: input.previousSuspendedLeafIds,
      dividers: [],
    };
  }

  const suspendedLeafIds = reconcileSuspension(
    sourceTree,
    input.previousSuspendedLeafIds,
    input.viewport,
    input.activeLeafId,
    input.focusOrder,
    pendingPreferredSplitIds,
    input.pinnedLeafIds,
    input.holdRestores,
  );
  const renderedTree = projectLayout(
    sourceTree,
    suspendedLeafIds,
    input.viewport,
    pendingPreferredSplitIds,
  );
  return {
    renderedTree,
    suspendedLeafIds,
    dividers: buildDividers(renderedTree, suspendedLeafIds),
  };
}

function findSplitNode(
  node: TerminalLayoutNode,
  splitId: string,
): Extract<TerminalLayoutNode, { type: 'split' }> | null {
  if (node.type !== 'split') {
    return null;
  }
  if (node.splitId === splitId) {
    return node;
  }
  return findSplitNode(node.children[0], splitId) || findSplitNode(node.children[1], splitId);
}

// A drag may fold any unfocused leaf past its minimum, so its floors assume
// everything on a side folds to slivers except the focused leaf.
export function dragRatioBounds(
  sourceTree: TerminalLayoutNode,
  splitId: string,
  protectedLeafId: string,
  spanPx: number,
): { min: number; max: number } {
  const split = findSplitNode(sourceTree, splitId);
  if (!split || spanPx <= 0) {
    return { min: 0.1, max: 0.9 };
  }
  const floor = (child: TerminalLayoutNode): number => {
    const foldable = new Set(
      collectLayoutLeaves(child).map(leafSlotId).filter((id) => id !== protectedLeafId),
    );
    const minimum = minimumSize(child, foldable);
    return split.direction === 'vertical' ? minimum.width : minimum.height;
  };
  return {
    min: Math.min(0.5, floor(split.children[0]) / spanPx),
    max: Math.max(0.5, 1 - (floor(split.children[1]) / spanPx)),
  };
}

function dragFoldsForSpan(
  side: TerminalLayoutNode,
  direction: 'vertical' | 'horizontal',
  spanPx: number,
  baselineSuspended: ReadonlySet<string>,
  protectedLeafId: string,
  staleness: Map<string, number>,
): Set<string> {
  const leafIds = collectLayoutLeaves(side).map(leafSlotId);
  const folded = new Set(baselineSuspended);
  const added = new Set<string>();
  const scores = intentSizeScores(side, new Set());
  for (;;) {
    const minimum = minimumSize(side, folded);
    const needed = direction === 'vertical' ? minimum.width : minimum.height;
    if (needed <= spanPx + 0.5) {
      break;
    }
    const victim = smallestLeaf(
      leafIds.filter((id) => !folded.has(id) && id !== protectedLeafId),
      scores,
      staleness,
    );
    if (!victim) {
      break;
    }
    folded.add(victim);
    added.add(victim);
  }
  return added;
}

export interface DragSuspensionInput {
  sourceTree: TerminalLayoutNode;
  splitId: string;
  ratio: number;
  splitBoxPx: { width: number; height: number };
  viewport: AttentionViewport;
  suspendedLeafIds: ReadonlySet<string>;
  pinnedLeafIds: ReadonlySet<string>;
  protectedLeafId: string;
  focusOrder: readonly string[];
}

export interface DragSuspensionResult {
  suspendedLeafIds: ReadonlySet<string>;
  pinnedLeafIds: ReadonlySet<string>;
}

// Each drag move re-derives the dragged split's pins from its side spans, so
// squeezing folds a leaf live and dragging back (or freeing room) restores it.
export function applyDragSuspension(input: DragSuspensionInput): DragSuspensionResult {
  const split = findSplitNode(input.sourceTree, input.splitId);
  if (!split) {
    return { suspendedLeafIds: input.suspendedLeafIds, pinnedLeafIds: input.pinnedLeafIds };
  }
  const axisPx = split.direction === 'vertical' ? input.splitBoxPx.width : input.splitBoxPx.height;
  const suspended = new Set(input.suspendedLeafIds);
  const pinned = new Set(input.pinnedLeafIds);
  const staleness = stalenessRank(
    collectLayoutLeaves(split).map(leafSlotId),
    input.focusOrder,
  );
  split.children.forEach((child, index) => {
    const sideLeafIds = new Set(collectLayoutLeaves(child).map(leafSlotId));
    for (const id of Array.from(pinned)) {
      if (sideLeafIds.has(id)) {
        pinned.delete(id);
        suspended.delete(id);
      }
    }
    const baseline = new Set([...suspended].filter((id) => sideLeafIds.has(id)));
    const spanPx = index === 0 ? axisPx * input.ratio : axisPx * (1 - input.ratio);
    const folds = dragFoldsForSpan(
      child,
      split.direction,
      spanPx,
      baseline,
      input.protectedLeafId,
      staleness,
    );
    for (const id of folds) {
      pinned.add(id);
      suspended.add(id);
    }
    // A baseline fold the dragged span cannot release, while the viewport
    // could, is held folded by this drag: pin it so drag-end restores skip it.
    const folded = new Set([...baseline, ...folds]);
    for (const id of baseline) {
      const released = new Set(folded);
      released.delete(id);
      const minimum = minimumSize(child, released);
      const needed = split.direction === 'vertical' ? minimum.width : minimum.height;
      if (needed <= spanPx + 0.5) {
        continue;
      }
      const restoredAll = new Set(suspended);
      restoredAll.delete(id);
      if (visibleLeavesFit(input.sourceTree, restoredAll, input.viewport, new Set())) {
        pinned.add(id);
      }
    }
  });
  return { suspendedLeafIds: suspended, pinnedLeafIds: pinned };
}

// Releasing never folds a peer here: reconcileSuspension picks the smallest
// unfocused victim, and only when space actually runs out.
export function releaseSuspendedLeaf(
  current: ReadonlySet<string>,
  targetLeafId: string,
): ReadonlySet<string> {
  if (!current.has(targetLeafId)) {
    return current;
  }
  const next = new Set(current);
  next.delete(targetLeafId);
  return next;
}

