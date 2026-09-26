import { useMemo } from 'react';
import type { AppView } from '../navigation/sessionNavigation';
import { useProfilesStore } from '../store/profiles';
import type { WorkspaceWithSessions } from '../utils/workspaceViewModels';
import { useAppGrid } from './useAppGrid';
import { useAppSessions } from './useAppSessions';

type EnrichedSession = ReturnType<typeof useAppSessions>['enrichedLocalSessions'][number];

interface Options {
  desktopViews: WorkspaceWithSessions<EnrichedSession>[];
  view: AppView;
  visibleGridTiles: ReturnType<typeof useAppGrid>['visibleGridTiles'];
  dragSourceDesktopId: string | null;
}
export function useDesktopResidency({ desktopViews, view, visibleGridTiles, dragSourceDesktopId }: Options) {
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const previousDesktopId = useProfilesStore((state) => state.previousDesktopId);
  const visibleGridSessionIds = useMemo(
    () => new Set(visibleGridTiles.map((tile) => tile.sessionId)),
    [visibleGridTiles],
  );
  const mountedDesktopIds = useMemo(() => {
    const mounted = new Set<string>();
    if (currentDesktopId) mounted.add(currentDesktopId);
    if (previousDesktopId) mounted.add(previousDesktopId);
    if (dragSourceDesktopId) mounted.add(dragSourceDesktopId);
    if (view === 'grid') {
      for (const group of desktopViews) {
        if (group.sessions.some((session) => visibleGridSessionIds.has(session.id))) {
          mounted.add(group.id);
        }
      }
    }
    return mounted;
  }, [currentDesktopId, previousDesktopId, dragSourceDesktopId, view, desktopViews, visibleGridSessionIds]);
  const onScreenSessionIds = useMemo(() => {
    if (view === 'grid') return visibleGridSessionIds;
    if (view !== 'session' || !currentDesktopId) return new Set<string>();
    const group = desktopViews.find((entry) => entry.id === currentDesktopId);
    return new Set((group?.sessions ?? []).map((session) => session.id));
  }, [view, visibleGridSessionIds, currentDesktopId, desktopViews]);

  return { onScreenSessionIds, mountedDesktopIds };
}
