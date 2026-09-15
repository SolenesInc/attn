import { useCallback, useEffect, useMemo, useState } from 'react';
import type { HiddenGridSession } from '../components/grid/GridHiddenSessions';
import { type GridSessionTile } from '../components/grid/GridView';
import {
  persistGridLayout,
  readGridLayout,
  resolveGridLayout,
  type GridLayout,
} from '../components/grid/gridLayout';
import {
  persistExcludedGridSessions,
  readExcludedGridSessions,
} from '../components/grid/gridMembership';
import type { useAppView } from '../hooks/useAppView';
import type { Session } from '../store/sessions';
import { type UISessionState } from '../types/sessionState';
interface Options {
  unmutedEnrichedSessions: (Session & { turnOwed?: boolean; crewMember?: string })[];
  wantsAttention: (session: {
    state: UISessionState;
    turnOwed?: boolean;
    crewMember?: string;
    automation?: { definition_id: string };
  }) => boolean;
  cancelPendingSelection: () => void;
  setView: ReturnType<typeof useAppView>['setView'];
}
export function useAppGrid({
  unmutedEnrichedSessions,
  wantsAttention,
  cancelPendingSelection,
  setView,
}: Options) {
  const gridSessionTiles = useMemo<GridSessionTile[]>(() => {
    const result: GridSessionTile[] = [];
    for (const s of unmutedEnrichedSessions) {
      const pane = s.workspace.agents.find((agent) => agent.sessionId === s.id);
      if (!pane) continue;
      const state = s.state;
      result.push({
        runtimeId: pane.runtimeId,
        sessionId: s.id,
        title: pane.title,
        state,
        attention: wantsAttention(s),
      });
    }
    return result;
  }, [unmutedEnrichedSessions, wantsAttention]);

  const [gridLayout, setGridLayout] = useState<GridLayout>(readGridLayout);
  const handleSelectGridLayout = useCallback(
    (layout: GridLayout) => {
      cancelPendingSelection();
      setGridLayout(layout);
      persistGridLayout(layout);
      setView('grid');
    },
    [cancelPendingSelection, setView],
  );

  const [excludedGridSessions, setExcludedGridSessions] =
    useState<Set<string>>(readExcludedGridSessions);
  const gridMembers = useMemo(
    () => gridSessionTiles.filter((t) => !excludedGridSessions.has(t.sessionId)),
    [gridSessionTiles, excludedGridSessions],
  );
  const hiddenGridSessions = useMemo<HiddenGridSession[]>(
    () =>
      gridSessionTiles
        .filter((t) => excludedGridSessions.has(t.sessionId))
        .map((t) => ({ sessionId: t.sessionId, title: t.title })),
    [gridSessionTiles, excludedGridSessions],
  );
  useEffect(() => {
    persistExcludedGridSessions(excludedGridSessions);
  }, [excludedGridSessions]);

  const handleRemoveFromGrid = useCallback((sessionId: string) => {
    setExcludedGridSessions((prev) => {
      if (prev.has(sessionId)) return prev;
      const next = new Set(prev);
      next.add(sessionId);
      return next;
    });
  }, []);
  const handleRestoreToGrid = useCallback((sessionId: string) => {
    setExcludedGridSessions((prev) => {
      if (!prev.has(sessionId)) return prev;
      const next = new Set(prev);
      next.delete(sessionId);
      return next;
    });
  }, []);

  const resolvedGridLayout = useMemo(
    () => resolveGridLayout(gridMembers.length, gridLayout),
    [gridMembers.length, gridLayout],
  );
  const visibleGridTiles = useMemo(
    () => gridMembers.slice(0, resolvedGridLayout.capacity),
    [gridMembers, resolvedGridLayout.capacity],
  );
  const gridOffBoardCount = gridMembers.length - visibleGridTiles.length;

  return {
    visibleGridTiles,
    gridLayout,
    handleSelectGridLayout,
    resolvedGridLayout,
    gridOffBoardCount,
    hiddenGridSessions,
    handleRemoveFromGrid,
    handleRestoreToGrid,
  };
}
