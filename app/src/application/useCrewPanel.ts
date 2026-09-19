import { useCallback, useState } from 'react';

interface CrewPanelState {
  open: boolean;
  member?: string;
  returnFocus?: HTMLElement;
  preserveStateOnOpen?: boolean;
}

export function useCrewPanel() {
  const [crewPanel, setCrewPanel] = useState<CrewPanelState>({
    open: false,
    preserveStateOnOpen: false,
  });

  const handleOpenCrew = useCallback((member: string | undefined, returnFocus: HTMLElement) => {
    setCrewPanel({ open: true, member, returnFocus, preserveStateOnOpen: false });
  }, []);

  const closeCrewPanel = useCallback(() => {
    setCrewPanel((current) => ({ ...current, open: false }));
  }, []);

  const handleCloseCrew = useCallback(() => {
    const returnFocus = crewPanel.returnFocus;
    closeCrewPanel();
    window.requestAnimationFrame(() => {
      if (returnFocus?.isConnected) returnFocus.focus();
    });
  }, [closeCrewPanel, crewPanel.returnFocus]);

  const handleBackToCrew = useCallback((returnFocus: HTMLElement) => {
    setCrewPanel((current) => ({ ...current, open: true, returnFocus, preserveStateOnOpen: true }));
  }, []);

  return { crewPanel, handleOpenCrew, closeCrewPanel, handleCloseCrew, handleBackToCrew };
}
