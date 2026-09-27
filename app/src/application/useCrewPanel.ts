import { useCallback, useState } from 'react';
import { useSessionStore } from '../store/sessions';

interface CrewPanelState {
  open: boolean;
  visit: number;
  member?: string;
  returnFocus?: HTMLElement;
}

export function useCrewPanel() {
  const [crewPanel, setCrewPanel] = useState<CrewPanelState>({ open: false, visit: 0 });

  const handleOpenCrew = useCallback((member: string | undefined, returnFocus: HTMLElement | undefined) => {
    setCrewPanel((current) => ({ open: true, visit: current.visit + 1, member, returnFocus }));
  }, []);

  const closeCrewPanel = useCallback(() => {
    setCrewPanel((current) => ({ ...current, open: false }));
  }, []);

  const handleCloseCrew = useCallback(() => {
    const returnFocus = crewPanel.returnFocus;
    closeCrewPanel();
    window.requestAnimationFrame(() => {
      if (returnFocus?.isConnected && returnFocus !== document.body) returnFocus.focus();
      else useSessionStore.getState().requestTerminalFocus();
    });
  }, [closeCrewPanel, crewPanel.returnFocus]);

  const handleBackToCrew = useCallback((returnFocus: HTMLElement) => {
    setCrewPanel((current) => ({ ...current, open: true, returnFocus }));
  }, []);

  return { crewPanel, handleOpenCrew, closeCrewPanel, handleCloseCrew, handleBackToCrew };
}
