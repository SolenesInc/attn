import { useSessionStore } from '../store/sessions';
import type { IntentTarget } from '../navigation/sessionNavigation';

interface OpenedTile {
  desktopId?: string | null;
  tileId?: string | null;
}

interface LandedLeaf {
  desktopId: string;
  leafId: string;
}

// Acts, then shows the leaf the daemon names in its answer, unless a later gesture or another client moved on.
export async function actThenShow<T>(target: IntentTarget, act: () => Promise<T>, landedLeaf: (result: T) => LandedLeaf | null): Promise<T> {
  const intent = useSessionStore.getState().beginIntent(target);
  let result: T;
  try {
    result = await act();
  } catch (error) {
    useSessionStore.getState().intentFailed(intent);
    throw error;
  }
  const leaf = landedLeaf(result);
  const navigation = useSessionStore.getState();
  if (navigation.intent?.id !== intent) return result;
  if (leaf) navigation.selectLeaf(leaf.desktopId, leaf.leafId, navigation.intent.focusOwner);
  else navigation.intentFailed(intent);
  return result;
}

export function openThenShow<T extends OpenedTile>(open: () => Promise<T>, landed?: (opened: T) => void): Promise<T> {
  return actThenShow({ kind: 'open' }, open, (opened) => {
    landed?.(opened);
    return opened.desktopId && opened.tileId ? { desktopId: opened.desktopId, leafId: opened.tileId } : null;
  });
}
