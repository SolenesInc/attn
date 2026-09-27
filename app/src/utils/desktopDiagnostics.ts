import type { TerminalLayoutNode, TerminalSplitDirection } from '../types/desktop';

export interface DesktopNormalizedBounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
  width: number;
  height: number;
}

export interface DesktopProjectedBounds {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface DesktopPaneLayoutDiagnostic {
  paneId: string;
  path: string;
  depth: number;
  bounds: DesktopNormalizedBounds;
}

export interface DesktopSplitLayoutDiagnostic {
  splitId: string;
  path: string;
  depth: number;
  direction: TerminalSplitDirection;
  ratio: number;
  spanCount: number;
  firstChildSpan: number;
  secondChildSpan: number;
  bounds: DesktopNormalizedBounds;
  firstChildPath: string;
  secondChildPath: string;
  firstChildBounds: DesktopNormalizedBounds;
  secondChildBounds: DesktopNormalizedBounds;
}

export interface DesktopLayoutDiagnosticSnapshot {
  paneCount: number;
  splitCount: number;
  panes: DesktopPaneLayoutDiagnostic[];
  splits: DesktopSplitLayoutDiagnostic[];
}

function clampSplitRatio(ratio: number): number {
  if (ratio > 0 && ratio < 1) {
    return ratio;
  }
  return 0.5;
}

function normalizedBounds(
  left: number,
  top: number,
  right: number,
  bottom: number,
): DesktopNormalizedBounds {
  return {
    left,
    top,
    right,
    bottom,
    width: right - left,
    height: bottom - top,
  };
}

function childPath(path: string, index: 0 | 1) {
  return `${path}/${index}`;
}

function chainSpanCount(node: TerminalLayoutNode, direction: TerminalSplitDirection): number {
  if (node.type !== 'split' || node.direction !== direction) {
    return 1;
  }
  return chainSpanCount(node.children[0], direction) + chainSpanCount(node.children[1], direction);
}

export function projectDesktopBounds(
  bounds: DesktopNormalizedBounds,
  rootWidth: number,
  rootHeight: number,
): DesktopProjectedBounds {
  return {
    x: Math.round(bounds.left * rootWidth),
    y: Math.round(bounds.top * rootHeight),
    width: Math.round(bounds.width * rootWidth),
    height: Math.round(bounds.height * rootHeight),
  };
}

export function collectDesktopLayoutDiagnostics(
  layoutTree: TerminalLayoutNode | null,
): DesktopLayoutDiagnosticSnapshot {
  const panes: DesktopPaneLayoutDiagnostic[] = [];
  const splits: DesktopSplitLayoutDiagnostic[] = [];
  if (!layoutTree) {
    return { paneCount: 0, splitCount: 0, panes, splits };
  }

  const walk = (
    node: TerminalLayoutNode,
    path: string,
    depth: number,
    bounds: DesktopNormalizedBounds,
  ) => {
    if (node.type !== 'split') {
      // Leaf node: record terminal panes; docked tiles occupy space but are
      // not part of pane diagnostics.
      if (node.type === 'pane') {
        panes.push({
          paneId: node.paneId,
          path,
          depth,
          bounds,
        });
      }
      return;
    }

    const ratio = clampSplitRatio(node.ratio);
    const firstPath = childPath(path, 0);
    const secondPath = childPath(path, 1);
    const firstChildSpan = chainSpanCount(node.children[0], node.direction);
    const secondChildSpan = chainSpanCount(node.children[1], node.direction);
    const firstChildBounds = node.direction === 'vertical'
      ? normalizedBounds(bounds.left, bounds.top, bounds.left + bounds.width * ratio, bounds.bottom)
      : normalizedBounds(bounds.left, bounds.top, bounds.right, bounds.top + bounds.height * ratio);
    const secondChildBounds = node.direction === 'vertical'
      ? normalizedBounds(firstChildBounds.right, bounds.top, bounds.right, bounds.bottom)
      : normalizedBounds(bounds.left, firstChildBounds.bottom, bounds.right, bounds.bottom);

    splits.push({
      splitId: node.splitId,
      path,
      depth,
      direction: node.direction,
      ratio,
      spanCount: firstChildSpan + secondChildSpan,
      firstChildSpan,
      secondChildSpan,
      bounds,
      firstChildPath: firstPath,
      secondChildPath: secondPath,
      firstChildBounds,
      secondChildBounds,
    });

    walk(node.children[0], firstPath, depth + 1, firstChildBounds);
    walk(node.children[1], secondPath, depth + 1, secondChildBounds);
  };

  walk(layoutTree, 'root', 0, normalizedBounds(0, 0, 1, 1));

  return {
    paneCount: panes.length,
    splitCount: splits.length,
    panes,
    splits,
  };
}
