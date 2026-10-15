import { useCallback, useEffect, useMemo, useState } from 'react';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import type { Seed } from '../../hooks/useDaemonSocket';
import { gardenPathToSeed, seedParentID } from '../../store/gardenWalk';
import { type SeedDocument } from '../SeedDocumentView';

interface PendingSeedNavigation {
  targetID: string;
  expectedPersistedPaths: string[];
}

interface Options {
  isSeed: boolean;
  persistedPath: string;
  baseTitle: string;
  gardenSeeds: Seed[];
  onUpdateParams?: (params: string) => Promise<unknown> | void;
}
export function useSeedTileNavigation({
  isSeed,
  persistedPath,
  baseTitle,
  gardenSeeds,
  onUpdateParams,
}: Options) {
  const [pendingSeedNavigation, setPendingSeedNavigation] = useState<PendingSeedNavigation | null>(
    null,
  );
  const [seedNavigationFailure, setSeedNavigationFailure] = useState<{
    path: string;
    message: string;
  } | null>(null);
  const [seedArrival, setSeedArrival] = useState<'in' | 'out'>('in');
  const pendingSeedID =
    pendingSeedNavigation &&
    pendingSeedNavigation.targetID !== persistedPath &&
    pendingSeedNavigation.expectedPersistedPaths.includes(persistedPath)
      ? pendingSeedNavigation.targetID
      : null;
  const path = isSeed && pendingSeedID ? pendingSeedID : persistedPath;
  const seedNavigationError =
    seedNavigationFailure?.path === path ? seedNavigationFailure.message : '';
  const { document: seedDocument, error: seedDocumentError, reload: reloadSeedDocument } = useLiveSeedDocument(
    path,
    gardenSeeds,
    isSeed,
  );
  const seedByID = useMemo(
    () => new Map(gardenSeeds.map((seed) => [seed.id, seed])),
    [gardenSeeds],
  );
  const seedPath = useMemo(
    () => (isSeed ? gardenPathToSeed(gardenSeeds, path) : []),
    [gardenSeeds, isSeed, path],
  );
  const parentSeed =
    seedPath.length > 1 ? (seedByID.get(seedPath[seedPath.length - 2]) ?? null) : null;
  const seedLocationTitle = seedPath.map((id) => seedByID.get(id)?.title || id).join(' › ');
  const navigateSeed = useCallback(
    (nextSeedID: string) => {
      if (!isSeed || !onUpdateParams || !nextSeedID || nextSeedID === path) return false;
      const currentSeed = seedDocument?.seed ?? seedByID.get(path);
      setSeedArrival(currentSeed && seedParentID(currentSeed) === nextSeedID ? 'out' : 'in');
      setSeedNavigationFailure(null);
      setPendingSeedNavigation((pending) => {
        const expectedPersistedPaths = pending?.expectedPersistedPaths.includes(persistedPath)
          ? [...pending.expectedPersistedPaths, pending.targetID]
          : [persistedPath];
        return { targetID: nextSeedID, expectedPersistedPaths };
      });
      void Promise.resolve(onUpdateParams(nextSeedID)).catch((navigationError) => {
        setPendingSeedNavigation((pending) => (pending?.targetID === nextSeedID ? null : pending));
        setSeedNavigationFailure({
          path: persistedPath,
          message:
            navigationError instanceof Error
              ? navigationError.message
              : `Could not open ${nextSeedID}`,
        });
      });
      return true;
    },
    [isSeed, onUpdateParams, path, persistedPath, seedByID, seedDocument],
  );

  const title = isSeed
    ? seedDocument?.seed.title || seedByID.get(path)?.title || path || baseTitle
    : baseTitle;
  return {
    path,
    seedNavigationError,
    seedDocument,
    seedDocumentError,
    reloadSeedDocument,
    title,
    parentSeed,
    seedLocationTitle,
    seedArrival,
    navigateSeed,
  };
}
function useLiveSeedDocument(seedId: string, gardenSeeds: Seed[], enabled: boolean) {
  const api = useOptionalDaemonApi();
  const sendSeedDocumentGet = api?.sendSeedDocumentGet;
  const connectionGeneration = api?.connectionGeneration ?? 0;
  const [document, setDocument] = useState<SeedDocument | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reloadRequest, setReloadRequest] = useState(0);
  const reload = useCallback(() => setReloadRequest((request) => request + 1), []);
  // Every garden fact re-pushes the seeds snapshot, and notes or child changes need not
  // touch this seed's own revision, so the array identity is the live invalidation signal.
  useEffect(() => {
    if (!enabled) return;
    if (!seedId) {
      setDocument(null);
      setError('No seed is associated with this tile.');
      return;
    }
    if (!sendSeedDocumentGet) {
      setDocument(null);
      setError('The daemon API is unavailable.');
      return;
    }
    let ignore = false;
    setError(null);
    void sendSeedDocumentGet(seedId)
      .then((next) => {
        if (ignore) return;
        setDocument(next);
      })
      .catch((readError) => {
        if (ignore) return;
        setError(readError instanceof Error ? readError.message : `Could not read ${seedId}`);
      });
    return () => {
      ignore = true;
    };
  }, [enabled, gardenSeeds, seedId, sendSeedDocumentGet, connectionGeneration, reloadRequest]);

  return { document: document?.seed.id === seedId ? document : null, error, reload };
}
