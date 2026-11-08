import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { Seed } from '../../hooks/useDaemonSocket';
import { useShortcut } from '../../shortcuts/useShortcut';
import { useDesktopShelfStore, type PublishedShelf, type ShelfColumn, type ShelfTile } from '../../store/desktopShelf';
import { useSessionStore } from '../../store/sessions';
import { tileContentKey, type TileContentState, type TileLeaf, type AgentTerminal } from '../../types/desktop';
import { sessionDisplayName } from '../../utils/sessionDisplayName';
import { deriveTileTitle } from '../../utils/tilePresentation';
import type { SessionTerminalDesktopProps } from './desktopTypes';
import {
  lastUsedLeaf,
  columnIndexOf,
  droppedBeside,
  columnsFitTogether,
  readRow,
  resolveRowLayout,
  revealDrop,
  revealFirstColumn,
  reachableTileCounts,
  stepNarrowestCols,
  type RowColumn,
  type RowLayoutInput,
  type RowLayoutPlan,
} from './rowLayout';

export const WIDTH_LABEL_MS = 1500;

type DesktopSession = NonNullable<SessionTerminalDesktopProps['desktopSessions']>[number];

interface ShelfPublisherInput {
  desktopId: string;
  rowPlan: RowLayoutPlan | null;
  rowInput: Omit<RowLayoutInput, 'view'> | null;
  showShelf: boolean;
  narrowestCols: number;
  tileHeightPx: number;
  activeLeafId: string;
  focusOrder: readonly string[];
  agentPaneById: ReadonlyMap<string, AgentTerminal>;
  sessionById: ReadonlyMap<string, DesktopSession>;
  tileLeafById: ReadonlyMap<string, TileLeaf>;
  tileContents?: Record<string, TileContentState>;
  gardenSeeds: readonly Seed[];
}

function columnTitle(titles: readonly string[]): string {
  if (titles.length <= 2) return titles.join(' + ');
  return `${titles[0]} + ${titles.length - 1}`;
}

export function useShelfPublisher({
  desktopId,
  rowPlan,
  rowInput,
  showShelf,
  narrowestCols,
  tileHeightPx,
  activeLeafId,
  focusOrder,
  agentPaneById,
  sessionById,
  tileLeafById,
  tileContents,
  gardenSeeds,
}: ShelfPublisherInput) {
  const shelf = useMemo<PublishedShelf | null>(() => {
    if (!rowPlan || !rowInput || !showShelf) return null;
    const activeColumn = rowPlan.screen.find((column) => column.leafIds.includes(activeLeafId));
    const seedTitle = (id: string) => gardenSeeds.find((seed) => seed.id === id)?.title;
    const shelfColumn = (column: RowColumn): ShelfColumn => {
      const tiles: ShelfTile[] = [];
      const titles: string[] = [];
      for (const leafId of column.leafIds) {
        const tile = tileLeafById.get(leafId);
        if (tile) {
          const title = deriveTileTitle(tile, tileContents?.[tileContentKey(desktopId, leafId)], seedTitle);
          tiles.push({ leafId, kind: tile.tileKind, title });
          titles.push(title);
          continue;
        }
        const pane = agentPaneById.get(leafId);
        const session = pane ? sessionById.get(pane.sessionId) : undefined;
        const title = session ? sessionDisplayName(session) : pane?.title || 'Terminal';
        tiles.push({
          leafId,
          kind: 'agent',
          title,
          terminalId: pane?.runtimeId,
          state: session?.state,
          offline: session?.offline,
          askHolder: session && {
            id: session.id,
            crewMember: session.crewMember,
            chiefOfStaff: session.chiefOfStaff,
            seedId: session.seedId,
          },
        });
        titles.push(title);
      }
      return {
        key: column.leafIds[0],
        goLeafId: lastUsedLeaf(column, focusOrder),
        label: columnTitle(titles),
        tiles,
        fitsBesideActive: activeColumn !== undefined && columnsFitTogether(activeColumn, column, rowInput),
      };
    };
    return {
      before: rowPlan.before.map(shelfColumn),
      screen: rowPlan.screen.map(shelfColumn),
      after: rowPlan.after.map(shelfColumn),
      activeLeafId,
      narrowestCols,
      tileHeightPx,
    };
  }, [activeLeafId, agentPaneById, desktopId, focusOrder, gardenSeeds, narrowestCols, rowInput, rowPlan, sessionById, showShelf, tileContents, tileHeightPx, tileLeafById]);

  useLayoutEffect(() => {
    useDesktopShelfStore.getState().publishShelf(desktopId, shelf);
  }, [desktopId, shelf]);
  useEffect(() => () => useDesktopShelfStore.getState().publishShelf(desktopId, null), [desktopId]);
}

interface RowSettlingInput {
  desktopId: string;
  rowPlan: RowLayoutPlan | null;
  rowInput: Omit<RowLayoutInput, 'view'> | null;
  measured: boolean;
  visible: boolean;
  activeLeafId: string;
  agentPaneById: ReadonlyMap<string, AgentTerminal>;
  tileLeafById: ReadonlyMap<string, TileLeaf>;
  onClearWantedCols: (columns: readonly RowColumn[]) => void;
}

const leavesOf = (columns: readonly RowColumn[]) => columns.flatMap((column) => column.leafIds);

function showRequestsOf(tiles: ReadonlyMap<string, TileLeaf>): ReadonlyMap<string, number> {
  return new Map([...tiles].map(([leafId, tile]) => [leafId, tile.showRequests ?? 0]));
}

export function useRowSettling(input: RowSettlingInput) {
  const latest = useRef(input);
  useLayoutEffect(() => {
    latest.current = input;
  });
  const { desktopId, rowPlan, measured, activeLeafId, tileLeafById: tiles } = input;
  const orderKey = rowPlan ? [...rowPlan.before, ...rowPlan.screen, ...rowPlan.after].map((column) => column.leafIds.join('+')).join('|') : '';
  const showRequestsKey = [...showRequestsOf(tiles)].join('|');
  const dropBeside = useDesktopShelfStore((state) => state.dropBesideByDesktop[desktopId]);

  useLayoutEffect(() => {
    const { rowPlan: plan, rowInput, visible, agentPaneById, tileLeafById, onClearWantedCols } = latest.current;
    if (!measured || !plan || !rowInput) return;
    const shelf = useDesktopShelfStore.getState();
    const previous = shelf.settledRowByDesktop[desktopId];
    const previousFirstColumn = shelf.firstColumnByDesktop[desktopId] ?? plan.firstColumn;
    const row = new Set(leavesOf([...plan.before, ...plan.screen, ...plan.after]));
    let firstColumn = plan.firstColumn;
    const showRequests = showRequestsOf(tileLeafById);
    const onScreen = new Set(leavesOf(plan.screen));
    const askedToShow = (leafId: string) => !onScreen.has(leafId) && (showRequests.get(leafId) ?? 0) !== (previous?.showRequests.get(leafId) ?? 0);
    const arrivals = new Set(previous ? [...row].filter((leafId) => !previous.row.has(leafId) || askedToShow(leafId)) : []);

    let pendingWantedCols = rowInput.pendingWantedCols;
    if (dropBeside && droppedBeside(rowInput, dropBeside)) {
      const reveal = revealDrop(rowInput, dropBeside, plan.firstColumn.index);
      if (reveal) {
        firstColumn = reveal.firstColumn;
        pendingWantedCols = reveal.pendingWantedCols;
        if (reveal.widthsToClear.length > 0) onClearWantedCols(reveal.widthsToClear);
      }
      shelf.setDropBeside(desktopId, null);
    }

    if (previous) {
      const returns = shelf.documentReturnsByDesktop[desktopId] ?? {};
      for (const leafId of previous.row) {
        const documentReturn = returns[leafId];
        if (row.has(leafId) || !documentReturn || previousFirstColumn.leafId !== documentReturn.shownAtLeafId) continue;
        if (columnIndexOf(readRow(rowInput.tree), documentReturn.firstColumn.leafId) < 0) continue;
        firstColumn = documentReturn.firstColumn;
        if (previous.activeLeafId === leafId && row.has(documentReturn.activeLeafId) && activeLeafId !== documentReturn.activeLeafId) {
          useSessionStore.getState().selectLeaf(desktopId, documentReturn.activeLeafId);
        }
      }

      for (const leafId of arrivals) {
        if (leafId === activeLeafId) continue;
        const tile = tileLeafById.get(leafId);
        if (!tile) continue;
        const owner = [...agentPaneById.values()].find((pane) => pane.sessionId === tile.tileSessionId);
        const ownerWasShown = owner !== undefined && previous.screen.has(owner.id);
        const revealed = !ownerWasShown ? null : onScreen.has(leafId) ? plan.firstColumn : revealFirstColumn(rowInput, leafId, plan.firstColumn.index);
        if (revealed) firstColumn = revealed;
        else if (!onScreen.has(leafId)) shelf.markUnseen(desktopId, leafId);
        shelf.saveDocumentReturn(desktopId, leafId, {
          firstColumn: previousFirstColumn,
          shownAtLeafId: (revealed ?? plan.firstColumn).leafId,
          activeLeafId: previous.activeLeafId,
        });
      }

      if (activeLeafId !== previous.activeLeafId && shelf.unseenByDesktop[desktopId]?.includes(activeLeafId)) {
        shelf.saveDocumentReturn(desktopId, activeLeafId, {
          firstColumn: previousFirstColumn,
          shownAtLeafId: firstColumn.leafId,
          activeLeafId: previous.activeLeafId,
        });
      }
    }

    const settledScreen = firstColumn === plan.firstColumn ? plan.screen : resolveRowLayout({ ...rowInput, pendingWantedCols, firstColumn }).screen;
    const screen = new Set(leavesOf(settledScreen));
    if (previous && visible) {
      const movedOff = (leafId: string) => previous.orderKey !== orderKey && previous.screen.has(leafId);
      shelf.flash(desktopId, [...row].filter((leafId) => !screen.has(leafId) && (arrivals.has(leafId) || movedOff(leafId))));
    }
    shelf.settleLeaves(desktopId, row, screen, activeLeafId);
    shelf.setFirstColumn(desktopId, firstColumn);
    shelf.settleRow(desktopId, { row, screen, activeLeafId, orderKey, showRequests });
  }, [activeLeafId, desktopId, dropBeside, measured, orderKey, showRequestsKey]);

  const screenKey = rowPlan ? leavesOf(rowPlan.screen).join('|') : '';
  useLayoutEffect(() => {
    const { rowPlan: plan } = latest.current;
    const shelf = useDesktopShelfStore.getState();
    if (!plan) {
      shelf.markSeen(desktopId, activeLeafId);
      return;
    }
    if (!measured) return;
    const row = new Set(leavesOf([...plan.before, ...plan.screen, ...plan.after]));
    const screen = new Set(leavesOf(plan.screen));
    shelf.settleScreen(desktopId, screen);
    shelf.settleLeaves(desktopId, row, screen, activeLeafId);
  }, [activeLeafId, desktopId, measured, screenKey]);
}

interface ShelfControlsInput {
  active: boolean;
  rowInput: Omit<RowLayoutInput, 'view'> | null;
  narrowestCols: number;
  onSetNarrowestCols?: (cols: number) => void;
  onClearWantedCols: (columns: readonly RowColumn[]) => void;
  onShelfLimit?: (message: string) => void;
}

export interface ShelfWidthLabelState {
  clearedWidths: number;
  tileCounts: number[];
}

export function useShelfControls({
  active,
  rowInput,
  narrowestCols,
  onSetNarrowestCols,
  onClearWantedCols,
  onShelfLimit,
}: ShelfControlsInput): ShelfWidthLabelState | null {
  const [clearedWidths, setClearedWidths] = useState<number | null>(null);
  const labelTimer = useRef<number | null>(null);
  useEffect(() => () => {
    if (labelTimer.current !== null) window.clearTimeout(labelTimer.current);
  }, []);

  const step = (direction: 'fewer' | 'more') => {
    if (!rowInput) return;
    const { narrowestPx: _narrowestPx, ...input } = rowInput;
    const result = stepNarrowestCols(input, narrowestCols, direction, window.innerWidth);
    if (result.kind === 'limit') {
      onShelfLimit?.(result.message);
      return;
    }
    const cleared = result.kind === 'clear-then-step' ? result.columns : [];
    onClearWantedCols(cleared);
    if (result.cols !== narrowestCols) onSetNarrowestCols?.(result.cols);
    setClearedWidths(cleared.length);
    if (labelTimer.current !== null) window.clearTimeout(labelTimer.current);
    labelTimer.current = window.setTimeout(() => setClearedWidths(null), WIDTH_LABEL_MS);
  };

  useShortcut('shelf.fewerTiles', () => step('fewer'), active);
  useShortcut('shelf.moreTiles', () => step('more'), active);
  if (clearedWidths === null || !rowInput) return null;
  const { narrowestPx: _narrowestPx, ...input } = rowInput;
  return { clearedWidths, tileCounts: reachableTileCounts(input, narrowestCols) };
}
