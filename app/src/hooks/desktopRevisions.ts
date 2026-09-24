import { useProfilesStore } from '../store/profiles';
import { ProfileCommandError } from './daemonProfileEvents';

export const FRESH_ARRANGEMENT_TRIPWIRE_MS = 5_000;

type Revisions = Map<string, number>;

function currentRevision(desktopId: string): number | null {
  return useProfilesStore.getState().desktops.find((desktop) => desktop.id === desktopId)?.revision ?? null;
}

function revisionsOf(desktopIds: string[]): Revisions {
  const revisions: Revisions = new Map();
  for (const desktopId of desktopIds) {
    const revision = currentRevision(desktopId);
    if (revision === null) throw new Error(`Desktop ${desktopId} is no longer part of this profile.`);
    revisions.set(desktopId, revision);
  }
  return revisions;
}

function arrangementAdvanced(seen: Revisions): boolean {
  return [...seen].some(([desktopId, revision]) => currentRevision(desktopId) !== revision);
}

export function waitForArrangementAfter(seen: Revisions): Promise<void> {
  if (arrangementAdvanced(seen)) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      unsubscribe();
      reject(new Error(
        `Another window changed this desktop and its new layout did not arrive within ${FRESH_ARRANGEMENT_TRIPWIRE_MS / 1000}s. Try again.`,
      ));
    }, FRESH_ARRANGEMENT_TRIPWIRE_MS);
    const unsubscribe = useProfilesStore.subscribe(() => {
      if (!arrangementAdvanced(seen)) return;
      window.clearTimeout(timer);
      unsubscribe();
      resolve();
    });
  });
}

export function isStaleRevision(error: unknown): boolean {
  return error instanceof ProfileCommandError && error.code === 'stale_revision';
}

export async function withFreshDesktopRevisions<T>(
  desktopIds: string[],
  send: (revisionOf: (desktopId: string) => number) => Promise<T>,
): Promise<T> {
  const seen = revisionsOf(desktopIds);
  const read = (revisions: Revisions) => (desktopId: string) => {
    const revision = revisions.get(desktopId);
    if (revision === undefined) throw new Error(`No revision was read for desktop ${desktopId}.`);
    return revision;
  };
  try {
    return await send(read(seen));
  } catch (error) {
    if (!isStaleRevision(error)) throw error;
    await waitForArrangementAfter(seen);
    return send(read(revisionsOf(desktopIds)));
  }
}
