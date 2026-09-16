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
  const { document: seedDocument, error: seedDocumentError } = useLiveSeedDocument(
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
    title,
    parentSeed,
    seedLocationTitle,
    seedArrival,
    navigateSeed,
  };
}
function useLiveSeedDocument(seedId: string, gardenSeeds: Seed[], enabled: boolean) {
  const sendSeedDocumentGet = useOptionalDaemonApi()?.sendSeedDocumentGet;
  const [document, setDocument] = useState<SeedDocument | null>(null);
  const [error, setError] = useState<string | null>(null);
  const liveSeed = useMemo(
    () => gardenSeeds.find((seed) => seed.id === seedId) ?? null,
    [gardenSeeds, seedId],
  );
  const displayedDocument = useMemo(() => {
    // A retarget is optimistic: tileParams catches up after the daemon persists
    // it, so never paint the old seed while the new one's document is in flight.
    if (!document || document.seed.id !== seedId) return null;
    if (!liveSeed || liveSeed.rev < document.seed.rev) return document;
    const tenderChanged =
      liveSeed.tender_session !== document.seed.tender_session ||
      liveSeed.tender_member !== document.seed.tender_member;
    return {
      ...document,
      seed: liveSeed,
      tender_holds: tenderChanged
        ? Boolean(liveSeed.tender_session || liveSeed.tender_member)
        : document.tender_holds,
    };
  }, [document, liveSeed, seedId]);

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
        if (liveSeed && liveSeed.rev >= next.seed.rev) {
          const tenderChanged =
            liveSeed.tender_session !== next.seed.tender_session ||
            liveSeed.tender_member !== next.seed.tender_member;
          setDocument({
            ...next,
            seed: liveSeed,
            tender_holds: tenderChanged
              ? Boolean(liveSeed.tender_session || liveSeed.tender_member)
              : next.tender_holds,
          });
        } else {
          setDocument(next);
        }
      })
      .catch((readError) => {
        if (ignore) return;
        setError(readError instanceof Error ? readError.message : `Could not read ${seedId}`);
      });
    return () => {
      ignore = true;
    };
  }, [enabled, gardenSeeds, liveSeed, seedId, sendSeedDocumentGet]);

  return { document: displayedDocument, error };
}
