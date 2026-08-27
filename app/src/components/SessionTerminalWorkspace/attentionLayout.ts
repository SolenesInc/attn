import {
  collectLayoutLeaves,
  getNormalizedPaneBounds,
  leafSlotId,
  type TerminalLayoutNode,
} from '../../types/workspace';

export const ATTENTION_MIN_WIDTH = 480;
export const ATTENTION_MIN_HEIGHT = 320;
export const ATTENTION_SLIVER_SIZE = 34;
const RESTORE_GUTTER = 24;

export interface AttentionViewport {
  width: number;
  height: number;
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

function weightedRatio(
  node: Extract<TerminalLayoutNode, { type: 'split' }>,
  suspended: ReadonlySet<string>,
  manualSplitIds: ReadonlySet<string>,
): number {
  const ratio = node.ratio > 0 && node.ratio < 1 ? node.ratio : 0.5;
  if (manualSplitIds.has(node.splitId) || !subtreeHasDocument(node)) {
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
  if (minimumTotal > span) {
    if (firstFullySuspended !== secondFullySuspended) {
      if (firstFullySuspended && firstMinimum < span) {
        return firstMinimum / span;
      }
      if (secondFullySuspended && secondMinimum < span) {
        return 1 - (secondMinimum / span);
      }
    }
    return firstMinimum / minimumTotal;
  }
  return Math.min(1 - (secondMinimum / span), Math.max(firstMinimum / span, ratio));
}

function projectNode(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
  manualSplitIds: ReadonlySet<string>,
  width: number,
  height: number,
): TerminalLayoutNode {
  if (node.type !== 'split') {
    return node;
  }

  const span = node.direction === 'vertical' ? width : height;
  const firstMinimum = minimumSize(node.children[0], suspended);
  const secondMinimum = minimumSize(node.children[1], suspended);
  const idealRatio = weightedRatio(node, suspended, manualSplitIds);
  const ratio = manualSplitIds.has(node.splitId)
    ? idealRatio
    : clampRatioToMinimums(
      idealRatio,
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
      projectNode(node.children[0], suspended, manualSplitIds, firstWidth, firstHeight),
      projectNode(node.children[1], suspended, manualSplitIds, secondWidth, secondHeight),
    ],
  };
}

export function projectAttentionLayout(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
  viewport: AttentionViewport,
  manualSplitIds: ReadonlySet<string> = new Set(),
): TerminalLayoutNode {
  return projectNode(node, suspended, manualSplitIds, viewport.width, viewport.height);
}

function visibleLeavesFit(
  node: TerminalLayoutNode,
  suspended: ReadonlySet<string>,
  viewport: AttentionViewport,
  manualSplitIds: ReadonlySet<string>,
  gutter = 0,
): boolean {
  const projected = projectAttentionLayout(node, suspended, viewport, manualSplitIds);
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

export function reconcileAttentionSuspension(
  node: TerminalLayoutNode,
  current: ReadonlySet<string>,
  viewport: AttentionViewport,
  protectedLeafId: string,
  focusOrder: readonly string[],
  manualSplitIds: ReadonlySet<string> = new Set(),
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

  while (!visibleLeavesFit(node, next, viewport, manualSplitIds)) {
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
    if (visibleLeavesFit(node, restored, viewport, manualSplitIds, RESTORE_GUTTER)) {
      next.delete(candidate);
    }
  }

  return sameIds(current, next) ? current : next;
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

export function splitIdsBesideSuspendedSubtrees(
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
