import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { Seed } from '../../hooks/useDaemonSocket';
import { useShortcut } from '../../shortcuts/useShortcut';
import { useDesktopShelfStore, type PublishedShelf, type ShelfColumn, type ShelfTile } from '../../store/desktopShelf';
import { tileContentKey, type TerminalLayoutNode, type TileContentState, type TileLeaf, type AgentTerminal } from '../../types/desktop';
import { sessionDisplayName } from '../../utils/sessionDisplayName';
import { deriveTileTitle } from '../../utils/tilePresentation';
import type { SessionTerminalDesktopProps } from './desktopTypes';
import {
  DEFAULT_NARROWEST_COLS,
  lastUsedLeaf,
  stepNarrowestCols,
  type FirstColumn,
  type RowColumn,
  type RowLayoutPlan,
} from './rowLayout';

const WIDTH_LABEL_MS = 1500;

type DesktopSession = NonNullable<SessionTerminalDesktopProps['desktopSessions']>[number];

interface ShelfPublisherInput {
  desktopId: string;
  rowPlan: RowLayoutPlan | null;
  showShelf: boolean;
  narrowestCols?: number;
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
          tiles.push({ leafId, kind: tile.tileKind });
          titles.push(deriveTileTitle(tile, tileContents?.[tileContentKey(desktopId, leafId)], seedTitle));
          continue;
        }
        const pane = agentPaneById.get(leafId);
        const session = pane ? sessionById.get(pane.sessionId) : undefined;
        tiles.push({
          leafId,
          kind: 'agent',
          state: session?.state,
          offline: session?.offline,
          askHolder: session && {
            id: session.id,
            crewMember: session.crewMember,
            chiefOfStaff: session.chiefOfStaff,
            seedId: session.seedId,
          },
        });
        titles.push(session ? sessionDisplayName(session) : pane?.title || 'Terminal');
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
    };
  }, [activeLeafId, agentPaneById, desktopId, focusOrder, gardenSeeds, narrowestCols, rowPlan, sessionById, showShelf, tileContents, tileLeafById]);

  useLayoutEffect(() => {
    useDesktopShelfStore.getState().publishShelf(desktopId, shelf);
  }, [desktopId, shelf]);
  useEffect(() => () => useDesktopShelfStore.getState().publishShelf(desktopId, null), [desktopId]);
}

interface ShelfControlsInput {
  active: boolean;
  tree: TerminalLayoutNode | null;
  activeLeafId: string;
  firstColumn: FirstColumn | null;
  areaPx: number;
  pxPerCol: number;
  narrowestCols?: number;
  onSetNarrowestCols?: (cols: number) => void;
  onShelfLimit?: (message: string) => void;
}

export function useShelfControls({
  active,
  tree,
  activeLeafId,
  firstColumn,
  areaPx,
  pxPerCol,
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
    if (!tree || narrowestCols === undefined) return;
    const result = stepNarrowestCols({ tree, activeLeafId, firstColumn, areaPx, pxPerCol }, narrowestCols, direction, window.innerWidth);
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
