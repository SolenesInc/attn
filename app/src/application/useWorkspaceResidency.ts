import { useEffect, useMemo, useState } from 'react';
import { useAppView } from '../hooks/useAppView';
import {
  computeWarmWorkspaceIds,
  DEFAULT_WARM_WORKSPACE_LIMIT,
  readWarmWorkspaceLimit,
  writeWarmWorkspaceLimit,
} from '../utils/terminalVirtualization';
import { useAppGrid } from './useAppGrid';
import { useAppSessions } from './useAppSessions';

import type { WorkspaceWithSessions } from '../utils/workspaceViewModels';
type EnrichedSession = ReturnType<typeof useAppSessions>['enrichedLocalSessions'][number];

interface Options {
  workspaceViews: WorkspaceWithSessions<EnrichedSession>[];
  activeWorkspaceId: string | null;
  view: ReturnType<typeof useAppView>['view'];
  visibleGridTiles: ReturnType<typeof useAppGrid>['visibleGridTiles'];
}
export function useWorkspaceResidency({
  workspaceViews,
  activeWorkspaceId,
  view,
  visibleGridTiles,
}: Options) {
  const [warmWorkspaceLimit, setWarmWorkspaceLimit] = useState<number>(() =>
    readWarmWorkspaceLimit(),
  );
  useEffect(() => {
    const w = window as Window & { attnSetWarmWorkspaces?: (n: number) => number };
    w.attnSetWarmWorkspaces = (n: number) => {
      const next = Number.isFinite(n) ? Math.trunc(n) : DEFAULT_WARM_WORKSPACE_LIMIT;
      writeWarmWorkspaceLimit(next);
      setWarmWorkspaceLimit(next);
      console.log(
        `[attn] warm workspace limit = ${next} ` +
          (next < 0
            ? '(virtualization disabled; all workspaces live)'
            : `(active + ${next} recent kept live)`),
      );
      return next;
    };
    return () => {
      delete w.attnSetWarmWorkspaces;
    };
  }, []);
  const [recentWorkspaceIds, setRecentWorkspaceIds] = useState<string[]>([]);
  useEffect(() => {
    if (!activeWorkspaceId) return;
    setRecentWorkspaceIds((prev) =>
      prev[0] === activeWorkspaceId
        ? prev
        : [activeWorkspaceId, ...prev.filter((id) => id !== activeWorkspaceId)].slice(0, 32),
    );
  }, [activeWorkspaceId]);
  const allWorkspaceIds = useMemo(() => workspaceViews.map((w) => w.id), [workspaceViews]);
  const visibleGridSessionIds = useMemo(
    () => new Set(visibleGridTiles.map((tile) => tile.sessionId)),
    [visibleGridTiles],
  );
  const gridVisibleWorkspaceIds = useMemo(
    () =>
      view === 'grid'
        ? workspaceViews
            .filter((workspace) =>
              workspace.sessions.some((session) => visibleGridSessionIds.has(session.id)),
            )
            .map((workspace) => workspace.id)
        : [],
    [view, visibleGridSessionIds, workspaceViews],
  );
  const onScreenSessionIds = useMemo(() => {
    if (view === 'grid') return visibleGridSessionIds;
    if (view !== 'session' || !activeWorkspaceId) return new Set<string>();
    const workspace = workspaceViews.find((w) => w.id === activeWorkspaceId);
    return new Set((workspace?.sessions ?? []).map((session) => session.id));
  }, [view, visibleGridSessionIds, activeWorkspaceId, workspaceViews]);

  const warmWorkspaceIds = useMemo(
    () =>
      computeWarmWorkspaceIds(
        allWorkspaceIds,
        recentWorkspaceIds,
        activeWorkspaceId,
        warmWorkspaceLimit,
        gridVisibleWorkspaceIds,
      ),
    [
      allWorkspaceIds,
      recentWorkspaceIds,
      activeWorkspaceId,
      warmWorkspaceLimit,
      gridVisibleWorkspaceIds,
    ],
  );

  return { onScreenSessionIds, warmWorkspaceIds };
}
