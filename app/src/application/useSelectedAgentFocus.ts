import { useEffect } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { desktopPaneOfAgent } from '../utils/desktops';

export function useSelectedAgentFocus(selectedSessionId: string | null) {
  const { sendDesktopSetActivePane } = useDaemonApi();
  useEffect(() => {
    if (!selectedSessionId) return;
    const { desktops, currentDesktopId } = useProfilesStore.getState();
    const pane = desktopPaneOfAgent(desktops, selectedSessionId);
    if (!pane) return;
    const desktop = desktops.find((entry) => entry.id === pane.desktop_id);
    if (pane.desktop_id === currentDesktopId && desktop?.active_pane_id === pane.pane_id) return;
    sendDesktopSetActivePane(pane.desktop_id, pane.pane_id).catch((error: unknown) => {
      console.warn('[App] Failed to focus the selected agent on its desktop:', error);
    });
  }, [selectedSessionId, sendDesktopSetActivePane]);
}
