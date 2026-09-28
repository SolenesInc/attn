import { useSessionStore } from '../store/sessions';

export interface TrackedOpen<T> {
  result: T;
  focusOwner: Element | null;
  moved: boolean;
}

// The epoch is read after dispatch: the open's own desktop command bumps it synchronously.
export async function trackOpen<T>(dispatch: () => Promise<T>): Promise<TrackedOpen<T>> {
  const focusOwner = typeof document === 'undefined' ? null : document.activeElement;
  const request = dispatch();
  const epoch = useSessionStore.getState().navigationEpoch;
  const result = await request;
  return { result, focusOwner, moved: useSessionStore.getState().navigationEpoch !== epoch };
}
