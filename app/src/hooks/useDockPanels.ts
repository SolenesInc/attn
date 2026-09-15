import { useCallback, useEffect, useReducer } from 'react';

export type DockPanelId = 'workflowRun' | 'attention' | 'automations' | 'garden';

const DOCK_PANEL_EXIT_MS = 260;

interface DockState {
  openPanels: Record<DockPanelId, boolean>;
  stack: DockPanelId[];
}

type DockAction = { type: 'open' | 'close' | 'toggle' | 'remove'; panelId: DockPanelId };

function reduceDock(state: DockState, { type, panelId }: DockAction): DockState {
  if (type === 'remove') {
    return state.openPanels[panelId]
      ? state
      : {
          ...state,
          stack: state.stack.filter((id) => id !== panelId),
        };
  }
  const open = type === 'toggle' ? !state.openPanels[panelId] : type === 'open';
  if (state.openPanels[panelId] === open) return state;
  return {
    openPanels: { ...state.openPanels, [panelId]: open },
    stack: open ? [...state.stack.filter((id) => id !== panelId), panelId] : state.stack,
  };
}

function usePanelExit(
  panelId: DockPanelId,
  state: DockState,
  remove: (panelId: DockPanelId) => void,
) {
  const exiting = !state.openPanels[panelId] && state.stack.includes(panelId);
  useEffect(() => {
    if (!exiting) return;
    const timer = setTimeout(() => remove(panelId), DOCK_PANEL_EXIT_MS);
    return () => clearTimeout(timer);
  }, [exiting, panelId, remove]);
}

export function useDockPanels() {
  const [dockState, dispatch] = useReducer(reduceDock, {
    openPanels: { workflowRun: false, attention: false, automations: false, garden: false },
    stack: [],
  });
  const remove = useCallback((panelId: DockPanelId) => dispatch({ type: 'remove', panelId }), []);
  usePanelExit('workflowRun', dockState, remove);
  usePanelExit('attention', dockState, remove);
  usePanelExit('automations', dockState, remove);
  usePanelExit('garden', dockState, remove);

  const openDockPanel = useCallback(
    (panelId: DockPanelId) => dispatch({ type: 'open', panelId }),
    [],
  );
  const closeDockPanel = useCallback(
    (panelId: DockPanelId) => dispatch({ type: 'close', panelId }),
    [],
  );
  const toggleDockPanel = useCallback(
    (panelId: DockPanelId) => dispatch({ type: 'toggle', panelId }),
    [],
  );
  return { dockState, openDockPanel, closeDockPanel, toggleDockPanel };
}
