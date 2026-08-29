import {
  applyRatioOverrides,
  collectLayoutLeaves,
  getNormalizedPaneBounds,
  getSplitDividers,
  hasLeaf,
  leafSlotId,
  type SplitDivider,
  type TerminalLayoutNode,
} from '../../types/workspace';

export const ATTENTION_MIN_WIDTH = 480;
export const ATTENTION_MIN_HEIGHT = 320;
export const ATTENTION_SLIVER_SIZE = 34;
const RESTORE_GUTTER = 24;
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

function reconcileSuspension(
  node: TerminalLayoutNode,
  current: ReadonlySet<string>,
  viewport: AttentionViewport,
  protectedLeafId: string,
  focusOrder: readonly string[],
  pendingPreferredSplitIds: ReadonlySet<string>,
): ReadonlySet<string> {
  const leafIds = collectLayoutLeaves(node).map(leafSlotId);
  const valid = new Set(leafIds);
  const next = new Set([...current].filter((id) => valid.has(id) && id !== protectedLeafId));
  if (viewport.width <= 0 || viewport.height <= 0) {
    return sameIds(current, next) ? current : next;
  }

  const orderedVictims = [
    ...[...focusOrder].reverse(),
    ...[...leafIds].reverse(),
  ].filter((id, index, all) => all.indexOf(id) === index);

  while (!visibleLeavesFit(node, next, viewport, pendingPreferredSplitIds)) {
    const visibleCount = leafIds.filter((id) => !next.has(id)).length;
    if (visibleCount <= 1) {
      break;
    }
    const victim = orderedVictims.find((id) => id !== protectedLeafId && !next.has(id));
    if (!victim) {
      break;
    }
    next.add(victim);
  }

  for (const candidate of [...next].reverse()) {
    const restored = new Set(next);
    restored.delete(candidate);
    if (visibleLeavesFit(node, restored, viewport, pendingPreferredSplitIds, RESTORE_GUTTER)) {
      next.delete(candidate);
    }
  }

  return sameIds(current, next) ? current : next;
}

function splitIdsBesideSuspendedSubtrees(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
  result = new Set<string>(),
): ReadonlySet<string> {
  if (node.type !== 'split') {
    return result;
  }
  if (
    allLeavesSuspended(node.children[0], suspended)
    || allLeavesSuspended(node.children[1], suspended)
  ) {
    result.add(node.splitId);
  }
  splitIdsBesideSuspendedSubtrees(node.children[0], suspended, result);
  splitIdsBesideSuspendedSubtrees(node.children[1], suspended, result);
  return result;
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
  );
  const renderedTree = projectLayout(
    sourceTree,
    suspendedLeafIds,
    input.viewport,
    pendingPreferredSplitIds,
  );
  const fixedSplitIds = splitIdsBesideSuspendedSubtrees(sourceTree, suspendedLeafIds);

  return {
    renderedTree,
    suspendedLeafIds,
    dividers: getSplitDividers(renderedTree).filter((divider) => !fixedSplitIds.has(divider.splitId)),
  };
}

export function swapSuspendedLeaf(
  current: ReadonlySet<string>,
  targetLeafId: string,
  previouslyFocusedLeafId: string,
): ReadonlySet<string> {
  if (!current.has(targetLeafId)) {
    return current;
  }
  const next = new Set(current);
  next.delete(targetLeafId);
  if (previouslyFocusedLeafId && previouslyFocusedLeafId !== targetLeafId) {
    next.add(previouslyFocusedLeafId);
  }
  return next;
}
