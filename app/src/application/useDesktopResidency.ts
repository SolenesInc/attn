import { useMemo } from 'react';
import type { AppView } from '../navigation/sessionNavigation';
import { useProfilesStore } from '../store/profiles';
import type { DesktopWithSessions } from '../utils/desktopViewModels';
import { useAppSessions } from './useAppSessions';

type EnrichedSession = ReturnType<typeof useAppSessions>['enrichedLocalSessions'][number];

interface Options {
  desktopViews: DesktopWithSessions<EnrichedSession>[];
  view: AppView;
  dragSourceDesktopId: string | null;
}
export function useDesktopResidency({ desktopViews, view, dragSourceDesktopId }: Options) {
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const previousDesktopId = useProfilesStore((state) => state.previousDesktopId);
  const mountedDesktopIds = useMemo(() => {
    const mounted = new Set<string>();
    if (currentDesktopId) mounted.add(currentDesktopId);
    if (previousDesktopId) mounted.add(previousDesktopId);
    if (dragSourceDesktopId) mounted.add(dragSourceDesktopId);
    return mounted;
  }, [currentDesktopId, previousDesktopId, dragSourceDesktopId]);
  const onScreenSessionIds = useMemo(() => {
    if (view !== 'session' || !currentDesktopId) return new Set<string>();
    const group = desktopViews.find((entry) => entry.id === currentDesktopId);
    return new Set((group?.sessions ?? []).map((session) => session.id));
  }, [view, currentDesktopId, desktopViews]);

  return { onScreenSessionIds, mountedDesktopIds };
}
