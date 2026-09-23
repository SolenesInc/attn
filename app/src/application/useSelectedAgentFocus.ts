import { useEffect } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { desktopPaneOfAgent } from '../utils/desktops';

export function useSelectedAgentFocus(selectedSessionId: string | null) {
  const { sendDesktopSetCurrent, sendDesktopSetActivePane } = useDaemonApi();
  useEffect(() => {
    if (!selectedSessionId) return;
    const { desktops, currentDesktopId, selectedProfileId } = useProfilesStore.getState();
    const pane = desktopPaneOfAgent(desktops, selectedSessionId);
    if (!pane || !selectedProfileId) return;
    const desktop = desktops.find((entry) => entry.id === pane.desktop_id);
    const onCurrentDesktop = pane.desktop_id === currentDesktopId;
    if (onCurrentDesktop && desktop?.active_pane_id === pane.pane_id) return;
    const focus = async () => {
      if (!onCurrentDesktop) await sendDesktopSetCurrent(selectedProfileId, pane.desktop_id);
      await sendDesktopSetActivePane(pane.desktop_id, pane.pane_id);
    };
    focus().catch((error: unknown) => {
      console.warn('[App] Failed to focus the selected agent on its desktop:', error);
    });
  }, [selectedSessionId, sendDesktopSetActivePane, sendDesktopSetCurrent]);
}
