import {
  hasLeaf,
  leafSlotId,
  type SplitDivider,
  type TerminalLayoutNode,
} from '../../types/desktop';

export const TILE_MIN_WIDTH = 480;
export const TILE_MIN_HEIGHT = 320;
const ZOOM_PATH_RATIO = 0.76;

export type DesktopLayoutView =
  | { mode: 'normal' }
  | { mode: 'focused'; leafId: string }
  | { mode: 'zoomed'; leafId: string };

export function buildDividers(node: TerminalLayoutNode): SplitDivider[] {
  const dividers: SplitDivider[] = [];
  const walk = (
    current: TerminalLayoutNode,
    left: number,
    top: number,
    right: number,
    bottom: number,
  ): void => {
    if (current.type !== 'split') {
      return;
    }
    const ratio = current.ratio > 0 && current.ratio < 1 ? current.ratio : 0.5;
    dividers.push({ splitId: current.splitId, direction: current.direction, ratio, left, top, right, bottom });
    if (current.direction === 'vertical') {
      const splitX = left + (right - left) * ratio;
      walk(current.children[0], left, top, splitX, bottom);
      walk(current.children[1], splitX, top, right, bottom);
    } else {
      const splitY = top + (bottom - top) * ratio;
      walk(current.children[0], left, top, right, splitY);
      walk(current.children[1], left, splitY, right, bottom);
    }
  };
  walk(node, 0, 0, 1, 1);
  return dividers;
}

export function findLeaf(node: TerminalLayoutNode, leafId: string): TerminalLayoutNode | null {
  if (node.type !== 'split') {
    return leafSlotId(node) === leafId ? node : null;
  }
  return findLeaf(node.children[0], leafId) || findLeaf(node.children[1], leafId);
}

export function zoomLayoutTowardLeaf(node: TerminalLayoutNode, leafId: string): TerminalLayoutNode {
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
