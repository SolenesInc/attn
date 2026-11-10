import type { ReviewBundle, TileWindowFrame } from './generated';

export type TerminalSplitDirection = 'vertical' | 'horizontal';
export type TerminalNavigationDirection = 'left' | 'right' | 'up' | 'down';
export type TerminalDockEdge = 'left' | 'right' | 'top' | 'bottom';
export type TerminalSplitRatioMode = 'automatic' | 'preferred';

export type TileKind = 'markdown' | 'seed' | 'browser' | (string & {});

export interface TerminalPaneLeaf {
  type: 'pane';
  paneId: string;
  wantedCols?: number;
  wantedAreaCols?: number;
  window?: TileWindowFrame;
}

export interface TileLeaf {
  type: 'tile';
  tileId: string;
  tileKind: TileKind;
  tileParams?: string;
  tileSessionId?: string;
  showRequests?: number;
  wantedCols?: number;
  wantedAreaCols?: number;
  window?: TileWindowFrame;
}

export type TerminalLeaf = TerminalPaneLeaf | TileLeaf;

export interface TileContentState {
  host?: string;
  path: string;
  content: string;
  error?: string;
  review?: ReviewBundle;
}

export function tileContentKey(desktopId: string, tileId: string): string {
  return `${desktopId}::${tileId}`;
}

export interface TerminalSplitNode {
  type: 'split';
  splitId: string;
  direction: TerminalSplitDirection;
  ratio: number;
  ratioMode?: TerminalSplitRatioMode;
  children: [TerminalLayoutNode, TerminalLayoutNode];
}

export type TerminalLayoutNode = TerminalPaneLeaf | TileLeaf | TerminalSplitNode;

export function collectLayoutLeaves(node: TerminalLayoutNode | null): TerminalLeaf[] {
  if (!node) {
    return [];
  }
  if (node.type !== 'split') {
    return [node];
  }
  return [
    ...collectLayoutLeaves(node.children[0]),
    ...collectLayoutLeaves(node.children[1]),
  ];
}

export function leafSlotId(node: TerminalLeaf): string {
  return node.type === 'pane' ? node.paneId : node.tileId;
}

export type PoppedLeaf = TerminalLeaf & { window: TileWindowFrame };

export function isPoppedLeaf(leaf: TerminalLeaf): leaf is PoppedLeaf {
  return leaf.window !== undefined;
}

export function poppedLeaves(tree: TerminalLayoutNode | null): PoppedLeaf[] {
  return collectLayoutLeaves(tree).filter(isPoppedLeaf);
}

function withoutPopped(node: TerminalLayoutNode): TerminalLayoutNode | null {
  if (node.type !== 'split') return isPoppedLeaf(node) ? null : node;
  const first = withoutPopped(node.children[0]);
  const second = withoutPopped(node.children[1]);
  if (!first || !second) return first ?? second;
  if (first === node.children[0] && second === node.children[1]) return node;
  return { ...node, children: [first, second] };
}

function withFirstLeafWidth(node: TerminalLayoutNode, width: Pick<TerminalLeaf, 'wantedCols' | 'wantedAreaCols'>): TerminalLayoutNode {
  if (node.type === 'split') {
    return { ...node, children: [withFirstLeafWidth(node.children[0], width), node.children[1]] };
  }
  const { wantedCols: _cols, wantedAreaCols: _areaCols, ...leaf } = node;
  return { ...leaf, ...width };
}

function mainColumn(column: TerminalLayoutNode): TerminalLayoutNode | null {
  const kept = withoutPopped(column);
  if (!kept || kept === column) return kept;
  const [{ wantedCols, wantedAreaCols }] = collectLayoutLeaves(column);
  return withFirstLeafWidth(kept, {
    ...(wantedCols !== undefined ? { wantedCols } : {}),
    ...(wantedAreaCols !== undefined ? { wantedAreaCols } : {}),
  });
}

export function mainTree(tree: TerminalLayoutNode | null): TerminalLayoutNode | null {
  if (!tree) return null;
  if (tree.type !== 'split' || tree.direction !== 'vertical') return mainColumn(tree);
  const first = mainTree(tree.children[0]);
  const second = mainTree(tree.children[1]);
  if (!first || !second) return first ?? second;
  if (first === tree.children[0] && second === tree.children[1]) return tree;
  return { ...tree, children: [first, second] };
}

export interface AgentTerminal {
  id: string;
  runtimeId: string;
  sessionId: string;
  title: string;
  status?: 'spawning' | 'ready' | 'failed';
  error?: string;
}

export interface TerminalDesktopState {
  agents: AgentTerminal[];
  layoutTree: TerminalLayoutNode | null;
}

export interface TerminalDesktopSnapshot {
  terminalState: TerminalDesktopState;
  daemonActivePaneId: string;
}

interface PaneBounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
  centerX: number;
  centerY: number;
}

export interface NormalizedPaneBounds extends PaneBounds {
  width: number;
  height: number;
}

export function createDefaultDesktopState(): TerminalDesktopState {
  return {
    agents: [],
    layoutTree: null,
  };
}

export function hasPane(node: TerminalLayoutNode, paneId: string): boolean {
  if (node.type === 'pane') {
    return node.paneId === paneId;
  }
  if (node.type === 'tile') {
    return false;
  }
  return hasPane(node.children[0], paneId) || hasPane(node.children[1], paneId);
}

export function hasLeaf(node: TerminalLayoutNode, leafId: string): boolean {
  if (node.type !== 'split') {
    return leafSlotId(node) === leafId;
  }
  return hasLeaf(node.children[0], leafId) || hasLeaf(node.children[1], leafId);
}

function paneBounds(
  left: number,
  top: number,
  right: number,
  bottom: number
): PaneBounds {
  return {
    left,
    top,
    right,
    bottom,
    centerX: (left + right) / 2,
    centerY: (top + bottom) / 2,
  };
}

function withDimensions(bounds: PaneBounds): NormalizedPaneBounds {
  return {
    ...bounds,
    width: bounds.right - bounds.left,
    height: bounds.bottom - bounds.top,
  };
}

function collectLeafBounds(
  node: TerminalLayoutNode,
  bounds: PaneBounds,
  result: Map<string, PaneBounds>
): void {
  if (node.type === 'pane') {
    result.set(node.paneId, bounds);
    return;
  }
  if (node.type === 'tile') {
    result.set(node.tileId, bounds);
    return;
  }

  const ratio = node.ratio > 0 && node.ratio < 1 ? node.ratio : 0.5;
  if (node.direction === 'vertical') {
    const splitX = bounds.left + (bounds.right - bounds.left) * ratio;
    collectLeafBounds(node.children[0], paneBounds(bounds.left, bounds.top, splitX, bounds.bottom), result);
    collectLeafBounds(node.children[1], paneBounds(splitX, bounds.top, bounds.right, bounds.bottom), result);
    return;
  }

  const splitY = bounds.top + (bounds.bottom - bounds.top) * ratio;
  collectLeafBounds(node.children[0], paneBounds(bounds.left, bounds.top, bounds.right, splitY), result);
  collectLeafBounds(node.children[1], paneBounds(bounds.left, splitY, bounds.right, bounds.bottom), result);
}

export function getNormalizedPaneBounds(node: TerminalLayoutNode): Map<string, NormalizedPaneBounds> {
  const rects = new Map<string, PaneBounds>();
  collectLeafBounds(node, paneBounds(0, 0, 1, 1), rects);
  return new Map(
    Array.from(rects.entries()).map(([slotId, bounds]) => [slotId, withDimensions(bounds)]),
  );
}

export interface SplitDivider {
  splitId: string;
  direction: TerminalSplitDirection;
  ratio: number;
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export function applyRatioOverrides(node: TerminalLayoutNode, overrides: ReadonlyMap<string, number>): TerminalLayoutNode {
  if (overrides.size === 0 || node.type !== 'split') {
    return node;
  }
  const override = overrides.get(node.splitId);
  return {
    ...node,
    ratio: override != null ? override : node.ratio,
    children: [
      applyRatioOverrides(node.children[0], overrides),
      applyRatioOverrides(node.children[1], overrides),
    ],
  };
}

export function collectSplitRatios(node: TerminalLayoutNode): Map<string, number> {
  const ratios = new Map<string, number>();
  const walk = (current: TerminalLayoutNode): void => {
    if (current.type !== 'split') {
      return;
    }
    ratios.set(current.splitId, current.ratio);
    walk(current.children[0]);
    walk(current.children[1]);
  };
  walk(node);
  return ratios;
}

export function collectPreferredSplitIds(node: TerminalLayoutNode): ReadonlySet<string> {
  const splitIds = new Set<string>();
  const walk = (current: TerminalLayoutNode): void => {
    if (current.type !== 'split') {
      return;
    }
    if (current.ratioMode === 'preferred') {
      splitIds.add(current.splitId);
    }
    walk(current.children[0]);
    walk(current.children[1]);
  };
  walk(node);
  return splitIds;
}

function overlapSize(aStart: number, aEnd: number, bStart: number, bEnd: number): number {
  return Math.max(0, Math.min(aEnd, bEnd) - Math.max(aStart, bStart));
}

export function findLeafInDirection(
  node: TerminalLayoutNode,
  fromLeafId: string,
  direction: TerminalNavigationDirection
): string | null {
  const rects = new Map<string, PaneBounds>();
  collectLeafBounds(node, paneBounds(0, 0, 1, 1), rects);
  const current = rects.get(fromLeafId);
  if (!current) {
    return null;
  }

  const candidates = Array.from(rects.entries())
    .filter(([leafId]) => leafId !== fromLeafId)
    .map(([leafId, candidate]) => {
      switch (direction) {
        case 'left': {
          const primary = current.left - candidate.right;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.top, current.bottom, candidate.top, candidate.bottom);
          if (overlap <= 0) return null;
          return { leafId, primary, secondary: Math.abs(current.centerY - candidate.centerY) };
        }
        case 'right': {
          const primary = candidate.left - current.right;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.top, current.bottom, candidate.top, candidate.bottom);
          if (overlap <= 0) return null;
          return { leafId, primary, secondary: Math.abs(current.centerY - candidate.centerY) };
        }
        case 'up': {
          const primary = current.top - candidate.bottom;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.left, current.right, candidate.left, candidate.right);
          if (overlap <= 0) return null;
          return { leafId, primary, secondary: Math.abs(current.centerX - candidate.centerX) };
        }
        case 'down': {
          const primary = candidate.top - current.bottom;
          if (primary < -1e-6) return null;
          const overlap = overlapSize(current.left, current.right, candidate.left, candidate.right);
          if (overlap <= 0) return null;
          return { leafId, primary, secondary: Math.abs(current.centerX - candidate.centerX) };
        }
      }
    })
    .filter((value): value is { leafId: string; primary: number; secondary: number } => Boolean(value))
    .sort((a, b) => {
      if (a.primary !== b.primary) {
        return a.primary - b.primary;
      }
      if (a.secondary !== b.secondary) {
        return a.secondary - b.secondary;
      }
      return a.leafId.localeCompare(b.leafId);
    });

  return candidates[0]?.leafId ?? null;
}

function parseWantedWidth(value: { wanted_cols?: unknown; wanted_area_cols?: unknown }): { wantedCols?: number; wantedAreaCols?: number } {
  if (typeof value.wanted_cols !== 'number' || value.wanted_cols <= 0) return {};
  return {
    wantedCols: value.wanted_cols,
    ...(typeof value.wanted_area_cols === 'number' && value.wanted_area_cols > 0 ? { wantedAreaCols: value.wanted_area_cols } : {}),
  };
}

function parseTileWindow(value: { window?: unknown }): { window?: TileWindowFrame } {
  const frame = value.window as Partial<Record<keyof TileWindowFrame, unknown>> | null | undefined;
  if (!frame || typeof frame !== 'object') return {};
  const { x, y, width, height } = frame;
  if (typeof x !== 'number' || typeof y !== 'number' || typeof width !== 'number' || typeof height !== 'number') return {};
  if (width <= 0 || height <= 0) return {};
  return { window: { x, y, width, height } };
}

function parseLayoutNode(raw: unknown): TerminalLayoutNode | null {
  if (!raw || typeof raw !== 'object') {
    return null;
  }
  const value = raw as Record<string, unknown>;
  if (value.type === 'pane' && typeof value.pane_id === 'string') {
    return {
      type: 'pane',
      paneId: value.pane_id,
      ...parseWantedWidth(value),
      ...parseTileWindow(value),
    };
  }
  if (
    value.type === 'tile' &&
    typeof value.tile_id === 'string' &&
    value.tile_id.length > 0 &&
    typeof value.tile_kind === 'string' &&
    value.tile_kind.length > 0
  ) {
    return {
      type: 'tile',
      tileId: value.tile_id,
      tileKind: value.tile_kind,
      tileParams: typeof value.tile_params === 'string' ? value.tile_params : undefined,
      tileSessionId: typeof value.tile_session_id === 'string' && value.tile_session_id.length > 0
        ? value.tile_session_id
        : undefined,
      showRequests: typeof value.show_requests === 'number' ? value.show_requests : undefined,
      ...parseWantedWidth(value),
      ...parseTileWindow(value),
    };
  }
  if (
    value.type === 'split' &&
    typeof value.split_id === 'string' &&
    (value.direction === 'vertical' || value.direction === 'horizontal') &&
    Array.isArray(value.children) &&
    value.children.length >= 2
  ) {
    const first = parseLayoutNode(value.children[0]);
    const second = parseLayoutNode(value.children[1]);
    if (!first || !second) {
      return null;
    }
    const ratio = typeof value.ratio === 'number' && value.ratio > 0 && value.ratio < 1 ? value.ratio : 0.5;
    return {
      type: 'split',
      splitId: value.split_id,
      direction: value.direction,
      ratio,
      ratioMode: value.ratio_mode === 'preferred' ? 'preferred' : 'automatic',
      children: [first, second],
    };
  }
  return null;
}

export function parseLayoutJSON(layoutJSON: string): TerminalLayoutNode | null {
  if (!layoutJSON.trim()) {
    return null;
  }
  try {
    const parsed = JSON.parse(layoutJSON);
    return parseLayoutNode(parsed);
  } catch {
    return null;
  }
}

export function tileIdsFromLayoutJSON(layoutJSON: string, kind?: TileKind): string[] {
  return collectLayoutLeaves(parseLayoutJSON(layoutJSON))
    .filter((leaf): leaf is TileLeaf => leaf.type === 'tile')
    .filter((tile) => !kind || tile.tileKind === kind)
    .map((tile) => tile.tileId);
}

export interface MarkdownTileParams {
  host?: string;
  path: string;
}

export function parseMarkdownTileParams(raw: string | undefined | null): MarkdownTileParams {
  if (!raw) {
    return { path: '' };
  }
  if (raw.startsWith('{')) {
    try {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      if (typeof parsed.host === 'string' && parsed.host && typeof parsed.path === 'string' && parsed.path) {
        return { host: parsed.host, path: parsed.path };
      }
    } catch {
      return { path: raw };
    }
  }
  return { path: raw };
}

export interface NotebookTileParams {
  root?: string;
  path?: string;
}

// Pre-arbitrary-roots tiles persisted a bare path string, and rootless tiles still
// round-trip through it, so a JSON-looking string that fails to parse is a legacy path.
export function parseNotebookTileParams(raw: string | undefined | null): NotebookTileParams {
  if (!raw) {
    return {};
  }
  if (raw.startsWith('{')) {
    try {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      const result: NotebookTileParams = {};
      if (typeof parsed.root === 'string' && parsed.root) {
        result.root = parsed.root;
      }
      if (typeof parsed.path === 'string' && parsed.path) {
        result.path = parsed.path;
      }
      return result;
    } catch {
      return { path: raw };
    }
  }
  return { path: raw };
}

// A rootless tile keeps persisting the legacy bare-path format so its params stay readable
// by code that still expects the pre-arbitrary-roots shape.
export function serializeNotebookTileParams(params: NotebookTileParams): string {
  if (params.root) {
    return JSON.stringify(params.path ? { root: params.root, path: params.path } : { root: params.root });
  }
  return params.path || '';
}

export function resolveEditorTileRoot(
  desktopDirectory: string | undefined,
  effectiveNotebookRoot: string,
): string | undefined {
  const trimmed = desktopDirectory?.trim();
  if (!trimmed || trimmed === effectiveNotebookRoot) {
    return undefined;
  }
  return trimmed;
}
