import { useSessionStore } from '../store/sessions';

interface OpenedTile {
  desktopId?: string | null;
  tileId?: string | null;
}

// Opens a tile the daemon names in its answer, then shows it unless a later gesture or another client moved on.
export async function openThenShow<T extends OpenedTile>(open: () => Promise<T>, landed?: (opened: T) => void): Promise<T> {
  const intent = useSessionStore.getState().beginIntent({ kind: 'open' });
  let opened: T;
  try {
    opened = await open();
  } catch (error) {
    useSessionStore.getState().intentFailed(intent);
    throw error;
  }
  landed?.(opened);
  const navigation = useSessionStore.getState();
  if (navigation.intent?.id !== intent) return opened;
  if (opened.desktopId && opened.tileId) navigation.selectLeaf(opened.desktopId, opened.tileId, navigation.intent.focusOwner);
  else navigation.intentFailed(intent);
  return opened;
}
