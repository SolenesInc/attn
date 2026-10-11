import { useMemo } from 'react';
import { useShallow } from 'zustand/react/shallow';
import type { AppView } from '../navigation/sessionNavigation';
import { useProfilesStore } from '../store/profiles';
import type { DesktopWithSessions } from '../utils/desktopViewModels';
import { useAppSessions } from './useAppSessions';
import { usePendingTerminalInput } from '../store/pendingTerminalInput';

type EnrichedSession = ReturnType<typeof useAppSessions>['enrichedLocalSessions'][number];

interface Options {
  desktopViews: DesktopWithSessions<EnrichedSession>[];
  view: AppView;
  dragSourceDesktopId: string | null;
}
export function useDesktopResidency({ desktopViews, view, dragSourceDesktopId }: Options) {
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const previousDesktopId = useProfilesStore((state) => state.previousDesktopId);
  const pendingSessionIds = usePendingTerminalInput(useShallow((state) => [...state.pending.keys()]));
  const mountedDesktopIds = useMemo(() => {
    const mounted = new Set<string>();
    if (currentDesktopId) mounted.add(currentDesktopId);
    if (previousDesktopId) mounted.add(previousDesktopId);
    if (dragSourceDesktopId) mounted.add(dragSourceDesktopId);
    for (const desktop of desktopViews) {
      if (desktop.sessions.some((session) => pendingSessionIds.includes(session.id))) mounted.add(desktop.id);
    }
    return mounted;
  }, [currentDesktopId, previousDesktopId, dragSourceDesktopId, desktopViews, pendingSessionIds]);
  const onScreenSessionIds = useMemo(() => {
    if (view !== 'session' || !currentDesktopId) return new Set<string>();
    const group = desktopViews.find((entry) => entry.id === currentDesktopId);
    return new Set((group?.sessions ?? []).map((session) => session.id));
  }, [view, currentDesktopId, desktopViews]);

  return { onScreenSessionIds, mountedDesktopIds };
}
