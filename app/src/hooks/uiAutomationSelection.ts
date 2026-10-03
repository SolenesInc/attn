import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { leafShows, type ShowTarget } from '../navigation/activeLeaf';
import { currentActiveLeaf } from './useDesktopSelectionBridge';

// Resolves once the request's intent is confirmed with the target shown, rejects when the app drops it.
export function selectionShown(target: ShowTarget): Promise<void> {
  const requestId = useSessionStore.getState().intent?.id ?? null;
  return new Promise((resolve, reject) => {
    const stops: Array<() => void> = [];
    const settle = (error?: Error) => {
      stops.forEach((stop) => stop());
      if (error) reject(error);
      else resolve();
    };
    const check = () => {
      if (requestId !== null && useSessionStore.getState().intent?.id === requestId) return;
      if (leafShows(currentActiveLeaf(), target) && useSessionStore.getState().view === 'session') {
        settle();
        return;
      }
      const shown = currentActiveLeaf();
      settle(new Error(`select ${JSON.stringify(target)}: the app dropped the request before the daemon showed it (shown is ${shown ? `${shown.desktopId}/${shown.leafId}` : 'none'})`));
    };
    stops.push(useProfilesStore.subscribe(check), useSessionStore.subscribe(check));
    check();
  });
}

// Resolves once the focus claim a confirmed show made is delivered with the keyboard in that leaf.
export function focusLanded(desktopId: string, leafId: string): Promise<void> {
  const claim = useSessionStore.getState().focusRequest;
  return new Promise((resolve, reject) => {
    let stop = () => {};
    const check = () => {
      if (claim && useSessionStore.getState().focusRequest?.id === claim.id) return;
      stop();
      const focused = document.activeElement;
      const pane = focused?.closest('[data-pane-id]');
      if (pane?.getAttribute('data-pane-id') === leafId && pane.closest(`[data-desktop-id="${desktopId}"]`)) {
        resolve();
        return;
      }
      reject(new Error(`focus ${desktopId}/${leafId}: the keyboard went to ${pane ? pane.getAttribute('data-pane-id') : focused?.tagName ?? 'nothing'} instead`));
    };
    stop = useSessionStore.subscribe(check);
    check();
  });
}
