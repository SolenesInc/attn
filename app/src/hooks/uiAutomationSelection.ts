import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { leafShows, type ShowTarget } from '../navigation/activeLeaf';
import { currentActiveLeaf } from './useDesktopSelectionBridge';

// Resolves once the current desktop's active leaf is the target, rejects when the app drops the request.
export function selectionShown(target: ShowTarget): Promise<void> {
  const requestId = useSessionStore.getState().pendingSelection?.id ?? null;
  return new Promise((resolve, reject) => {
    const stops: Array<() => void> = [];
    const settle = (error?: Error) => {
      stops.forEach((stop) => stop());
      if (error) reject(error);
      else resolve();
    };
    const check = () => {
      if (leafShows(currentActiveLeaf(), target)) {
        settle();
        return;
      }
      if (useSessionStore.getState().pendingSelection?.id !== requestId || requestId === null) {
        const shown = currentActiveLeaf();
        settle(new Error(`select ${JSON.stringify(target)}: the app dropped the request before the daemon showed it (shown is ${shown ? `${shown.desktopId}/${shown.leafId}` : 'none'})`));
      }
    };
    stops.push(useProfilesStore.subscribe(check), useSessionStore.subscribe(check));
    check();
  });
}
