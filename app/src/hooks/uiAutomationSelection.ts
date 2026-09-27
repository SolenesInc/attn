import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';

function onCurrentDesktop(sessionId: string): boolean {
  const { desktops, currentDesktopId } = useProfilesStore.getState();
  const current = desktops.find((desktop) => desktop.id === currentDesktopId);
  return current?.panes.some((pane) => pane.session_id === sessionId) ?? false;
}

function stillWanted(sessionId: string): boolean {
  const { activeSessionId, pendingSelection } = useSessionStore.getState();
  return activeSessionId === sessionId || pendingSelection?.sessionId === sessionId;
}

// Selecting shows a session only once the daemon makes its desktop current; a read before
// that sees the previous desktop. Resolves on that broadcast, rejects if the app gives up.
export function selectionShown(sessionId: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const stops: Array<() => void> = [];
    const check = () => {
      if (onCurrentDesktop(sessionId)) {
        stops.forEach((stop) => stop());
        resolve();
      } else if (!stillWanted(sessionId)) {
        stops.forEach((stop) => stop());
        reject(new Error(`select ${sessionId}: the app abandoned the selection before its desktop became current (active is ${useSessionStore.getState().activeSessionId ?? 'none'})`));
      }
    };
    stops.push(useProfilesStore.subscribe(check), useSessionStore.subscribe(check));
    check();
  });
}
