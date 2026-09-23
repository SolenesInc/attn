import { useEffect } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { desktopPaneOfAgent } from '../utils/desktops';

export function useSelectedAgentFocus(selectedSessionId: string | null) {
  const { sendDesktopFocusSession } = useDaemonApi();
  useEffect(() => {
    if (!selectedSessionId) return;
    const { desktops, currentDesktopId } = useProfilesStore.getState();
    const pane = desktopPaneOfAgent(desktops, selectedSessionId);
    const current = desktops.find((desktop) => desktop.id === currentDesktopId);
    if (pane && current?.id === pane.desktop_id && current.active_pane_id === pane.pane_id) return;
    sendDesktopFocusSession(selectedSessionId).catch((error: unknown) => {
      console.warn('[App] Failed to focus the selected agent:', error);
    });
  }, [selectedSessionId, sendDesktopFocusSession]);
}
