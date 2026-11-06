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
  DEFAULT_NARROWEST_COLS,
  lastUsedLeaf,
  columnIndexOf,
  firstColumnStillShows,
  readRow,
  resolveRowLayout,
  revealFirstColumn,
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
  showShelf: boolean;
  narrowestCols?: number;
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
    if (!rowPlan || !showShelf) return null;
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
      };
    };
    return {
      before: rowPlan.before.map(shelfColumn),
      screen: rowPlan.screen.map(shelfColumn),
      after: rowPlan.after.map(shelfColumn),
      activeLeafId,
      narrowestCols: narrowestCols ?? DEFAULT_NARROWEST_COLS,
      tileHeightPx,
    };
  }, [activeLeafId, agentPaneById, desktopId, focusOrder, gardenSeeds, narrowestCols, rowPlan, sessionById, showShelf, tileContents, tileHeightPx, tileLeafById]);

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
}

const leavesOf = (columns: readonly RowColumn[]) => columns.flatMap((column) => column.leafIds);

export function useRowSettling(input: RowSettlingInput) {
  const latest = useRef(input);
  useLayoutEffect(() => {
    latest.current = input;
  });
  const { desktopId, rowPlan, measured, activeLeafId } = input;
  const orderKey = rowPlan ? [...rowPlan.before, ...rowPlan.screen, ...rowPlan.after].map((column) => column.leafIds.join('+')).join('|') : '';

  useLayoutEffect(() => {
    const { rowPlan: plan, rowInput, visible, agentPaneById, tileLeafById } = latest.current;
    if (!measured || !plan || !rowInput) return;
    const shelf = useDesktopShelfStore.getState();
    const previous = shelf.settledRowByDesktop[desktopId];
    const previousFirstColumn = shelf.firstColumnByDesktop[desktopId] ?? plan.firstColumn;
    const row = new Set(leavesOf([...plan.before, ...plan.screen, ...plan.after]));
    let firstColumn = plan.firstColumn;

    if (previous) {
      const returns = shelf.documentReturnsByDesktop[desktopId] ?? {};
      for (const leafId of previous.row) {
        const documentReturn = returns[leafId];
        if (row.has(leafId) || !documentReturn || previousFirstColumn.leafId !== documentReturn.shownAtLeafId) continue;
        if (columnIndexOf(readRow(rowInput.tree), documentReturn.firstColumn.leafId) < 0) continue;
        firstColumn = documentReturn.firstColumn;
        if (previous.activeLeafId === leafId && row.has(documentReturn.activeLeafId) && !firstColumnStillShows(rowInput, firstColumn)) {
          useSessionStore.getState().selectLeaf(desktopId, documentReturn.activeLeafId, undefined, true);
        }
      }

      const onScreen = new Set(leavesOf(plan.screen));
      for (const leafId of row) {
        if (previous.row.has(leafId) || leafId === activeLeafId) continue;
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
          shownAtLeafId: plan.firstColumn.leafId,
          activeLeafId: previous.activeLeafId,
        });
      }
    }

    const settledScreen = firstColumn === plan.firstColumn ? plan.screen : resolveRowLayout({ ...rowInput, firstColumn }).screen;
    const screen = new Set(leavesOf(settledScreen));
    if (previous && visible && previous.orderKey !== orderKey) {
      shelf.flash(desktopId, [...row].filter((leafId) => !screen.has(leafId) && (previous.screen.has(leafId) || !previous.row.has(leafId))));
    }
    shelf.settleLeaves(desktopId, row, screen, activeLeafId);
    shelf.setFirstColumn(desktopId, firstColumn);
    shelf.settleRow(desktopId, { row, screen, activeLeafId, orderKey });
  }, [activeLeafId, desktopId, measured, orderKey]);

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
  narrowestCols?: number;
  onSetNarrowestCols?: (cols: number) => void;
  onShelfLimit?: (message: string) => void;
}

export function useShelfControls({
  active,
  rowInput,
  narrowestCols,
  onSetNarrowestCols,
  onShelfLimit,
}: ShelfControlsInput): boolean {
  const [labelShown, setLabelShown] = useState(false);
  const labelTimer = useRef<number | null>(null);
  useEffect(() => () => {
    if (labelTimer.current !== null) window.clearTimeout(labelTimer.current);
  }, []);

  const step = (direction: 'fewer' | 'more') => {
    if (!rowInput || narrowestCols === undefined) return;
    const { narrowestPx: _narrowestPx, ...input } = rowInput;
    const result = stepNarrowestCols(input, narrowestCols, direction, window.innerWidth);
    if (result.kind === 'limit') {
      onShelfLimit?.(result.message);
      return;
    }
    onSetNarrowestCols?.(result.cols);
    setLabelShown(true);
    if (labelTimer.current !== null) window.clearTimeout(labelTimer.current);
    labelTimer.current = window.setTimeout(() => setLabelShown(false), WIDTH_LABEL_MS);
  };

  useShortcut('shelf.fewerTiles', () => step('fewer'), active);
  useShortcut('shelf.moreTiles', () => step('more'), active);
  return labelShown;
}
