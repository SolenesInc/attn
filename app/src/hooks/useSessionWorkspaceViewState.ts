import { useCallback } from 'react';
import { useSessionStore, type Session } from '../store/sessions';
import { activeWorkspacePane } from '../navigation/workspacePaneSelection';

export function useSessionWorkspaceViewState() {
  const {
    workspacePaneSelections,
    setActivePane,
    prepareClosePaneFocus,
    clearPreparedClosePaneFocus,
  } = useSessionStore();
  const getActivePaneIdForSession = useCallback(
    (session: Session | undefined | null) => activeWorkspacePane(workspacePaneSelections, session),
    [workspacePaneSelections],
  );
  const prepareClosePaneFocusForSession = useCallback(
    (session: Session | undefined | null, paneId: string) =>
      session ? prepareClosePaneFocus(session.id, paneId) : '',
    [prepareClosePaneFocus],
  );
  return {
    getActivePaneIdForSession,
    setActivePane,
    prepareClosePaneFocus: prepareClosePaneFocusForSession,
    clearPreparedClosePaneFocus,
  };
}
