import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';

export interface TrackedOpen<T> {
  result: T;
  focusOwner: Element | null;
  moved: boolean;
}

type Own = <R>(send: () => Promise<R>) => Promise<R>;

const epoch = () => useSessionStore.getState().navigationEpoch;

// Commands the open sends through `own` (retries included) do not count as the user moving on.
export async function trackOpen<T>(dispatch: (own: Own) => Promise<T>): Promise<TrackedOpen<T>> {
  const focusOwner = typeof document === 'undefined' ? null : document.activeElement;
  const start = epoch();
  let ownBumps = 0;
  const own: Own = (send) => {
    const before = epoch();
    const request = send();
    ownBumps += epoch() - before;
    return request;
  };
  const result = await dispatch(own);
  return { result, focusOwner, moved: epoch() - start !== ownBumps };
}

export async function showDesktop(desktopId: string, send: () => Promise<unknown>): Promise<void> {
  const leafId = useProfilesStore.getState().desktops.find((desktop) => desktop.id === desktopId)?.active_pane_id;
  const { moved, focusOwner } = await trackOpen((own) => own(send));
  if (!moved && leafId) useSessionStore.getState().claimLeafFocus(desktopId, leafId, focusOwner);
}
