import { useMemo } from 'react';
import { useShallow } from 'zustand/react/shallow';
import type { AppView } from '../navigation/sessionNavigation';
import { useProfilesStore } from '../store/profiles';
import type { DesktopWithSessions } from '../utils/desktopViewModels';
import { useAppSessions } from './useAppSessions';
import { usePendingTerminalInput } from '../store/pendingTerminalInput';
import { poppedLeaves } from '../types/desktop';
import { desktopTerminalState } from '../utils/desktops';

type EnrichedSession = ReturnType<typeof useAppSessions>['enrichedLocalSessions'][number];

interface Options {
  desktopViews: DesktopWithSessions<EnrichedSession>[];
  view: AppView;
  dragSourceDesktopId: string | null;
  tileWindowsEnabled: boolean;
}

function poppedPanes(desktops: ReturnType<typeof useProfilesStore.getState>['desktops'], enabled: boolean) {
  if (!enabled) return { desktopIds: new Set<string>(), sessionIds: new Set<string>() };
  const desktopIds = new Set<string>();
  const sessionIds = new Set<string>();
  for (const desktop of desktops) {
    const state = desktopTerminalState(desktop);
    for (const leaf of poppedLeaves(state.layoutTree)) {
      desktopIds.add(desktop.id);
      if (leaf.type !== 'pane') continue;
      const sessionId = state.agents.find((agent) => agent.id === leaf.paneId)?.sessionId;
      if (sessionId) sessionIds.add(sessionId);
    }
  }
  return { desktopIds, sessionIds };
}

export function useDesktopResidency({ desktopViews, view, dragSourceDesktopId, tileWindowsEnabled }: Options) {
  const desktops = useProfilesStore((state) => state.desktops);
  const popped = useMemo(() => poppedPanes(desktops, tileWindowsEnabled), [desktops, tileWindowsEnabled]);
  const currentDesktopId = useProfilesStore((state) => state.currentDesktopId);
  const previousDesktopId = useProfilesStore((state) => state.previousDesktopId);
  const pendingSessionIds = usePendingTerminalInput(useShallow((state) => [...state.pending.keys()]));
  const mountedDesktopIds = useMemo(() => {
    const mounted = new Set<string>();
    if (currentDesktopId) mounted.add(currentDesktopId);
    if (previousDesktopId) mounted.add(previousDesktopId);
    if (dragSourceDesktopId) mounted.add(dragSourceDesktopId);
    for (const desktopId of popped.desktopIds) mounted.add(desktopId);
    for (const desktop of desktopViews) {
      if (desktop.sessions.some((session) => pendingSessionIds.includes(session.id))) mounted.add(desktop.id);
    }
    return mounted;
  }, [currentDesktopId, previousDesktopId, dragSourceDesktopId, desktopViews, pendingSessionIds, popped]);
  const onScreenSessionIds = useMemo(() => {
    const onScreen = new Set(popped.sessionIds);
    if (view !== 'session' || !currentDesktopId) return onScreen;
    const group = desktopViews.find((entry) => entry.id === currentDesktopId);
    for (const session of group?.sessions ?? []) onScreen.add(session.id);
    return onScreen;
  }, [view, currentDesktopId, desktopViews, popped]);

  return { onScreenSessionIds, mountedDesktopIds };
}
