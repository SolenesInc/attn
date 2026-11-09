import { useCallback } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { type SeedPlacement, type SeedReviewActionContext } from '../hooks/useDaemonSocket';
import { useDockPanels } from '../hooks/useDockPanels';
import { useDaemonStore } from '../store/daemonSessions';
import { gardenPathToSeed, useGardenWalk } from '../store/gardenWalk';
import { crewDisplayName } from '../utils/crewName';
import { openThenShow } from './openThenShow';
interface Options {
  sendOpenSeed: ReturnType<typeof useDaemonApi>['sendOpenSeed'];
  contextSessionId: string | null;
  showError: ReturnType<typeof useToast>['showError'];
  seeds: ReturnType<typeof useDaemonStore.getState>['seeds'];
  openDockPanel: ReturnType<typeof useDockPanels>['openDockPanel'];
  sendFsExists: ReturnType<typeof useDaemonApi>['sendFsExists'];
  sendOpenMarkdown: ReturnType<typeof useDaemonApi>['sendOpenMarkdown'];
  sendSeedResume: ReturnType<typeof useDaemonApi>['sendSeedResume'];
  handleSelectSession: (sessionId: string) => boolean;
  sendSeedHandover: ReturnType<typeof useDaemonApi>['sendSeedHandover'];
  sendSeedToChief: ReturnType<typeof useDaemonApi>['sendSeedToChief'];
  sendCrewStart: ReturnType<typeof useDaemonApi>['sendCrewStart'];
  sendCrewClose: ReturnType<typeof useDaemonApi>['sendCrewClose'];
  setCrewSeedTile: (tile: { desktopId: string; tileId: string } | null) => void;
  closeCrewPanel: () => void;
}
export function useAppGardenActions({
  sendOpenSeed,
  contextSessionId,
  showError,
  seeds,
  openDockPanel,
  sendFsExists,
  sendOpenMarkdown,
  sendSeedResume,
  handleSelectSession,
  sendSeedHandover,
  sendSeedToChief,
  sendCrewStart,
  sendCrewClose,
  setCrewSeedTile,
  closeCrewPanel,
}: Options) {
  const openSeedTile = useCallback(
    async (
      seedId: string,
      placement: SeedPlacement,
      beforeFocus?: (opened: { desktopId: string; tileId: string }) => void,
    ) => {
      const opened = await openThenShow(() => sendOpenSeed(seedId, placement), ({ desktopId, tileId }) => {
        if (desktopId && tileId) beforeFocus?.({ desktopId, tileId });
      });
      if (!opened.desktopId || !opened.tileId) {
        throw new Error(`The daemon opened ${seedId} without a desktop tile`);
      }
      return opened;
    },
    [sendOpenSeed],
  );

  const handleOpenSeedTile = useCallback(
    (seedId: string) => {
      void openSeedTile(seedId, { sessionId: contextSessionId || '' }).catch((error) => {
        showError(error instanceof Error ? error.message : 'Could not open the seed');
      });
    },
    [contextSessionId, openSeedTile, showError],
  );

  const handleOpenSeedFromCrew = useCallback(
    (seedId: string, placementSessionId?: string) => {
      void openSeedTile(
        seedId,
        placementSessionId ? { sessionId: placementSessionId } : 'standalone',
        (opened) => {
          setCrewSeedTile(opened);
          closeCrewPanel();
        },
      ).catch((error) => {
        showError(error instanceof Error ? error.message : 'Could not open the seed');
      });
    },
    [closeCrewPanel, openSeedTile, setCrewSeedTile, showError],
  );

  const handleRevealSeedInGarden = useCallback(
    (seedId: string) => {
      const trail = gardenPathToSeed(seeds, seedId);
      if (trail.length === 0) {
        handleOpenSeedTile(seedId);
        return;
      }
      useGardenWalk.getState().arriveAt(trail);
      openDockPanel('garden');
    },
    [handleOpenSeedTile, openDockPanel, seeds],
  );

  const checkArtifactPath = useCallback(
    (path: string) => {
      const slash = path.lastIndexOf('/');
      if (slash <= 0) return Promise.resolve(true);
      return sendFsExists(path.slice(slash + 1), path.slice(0, slash)).then(
        (result) => result.exists,
      );
    },
    [sendFsExists],
  );

  const handleOpenMarkdownArtifact = useCallback(
    (path: string) => {
      void openThenShow(() => sendOpenMarkdown(path, ''))
        .catch((error) => {
          showError(error instanceof Error ? error.message : 'Could not open the document');
        });
    },
    [sendOpenMarkdown, showError],
  );

  const handleResumeSeed = useCallback(
    (seedId: string, review?: SeedReviewActionContext) => {
      const resume = sendSeedResume(seedId, review).then((result) => {
        handleSelectSession(result.sessionId);
        return result;
      });
      if (review) return resume;
      return resume.catch((error) => {
        showError(error instanceof Error ? error.message : 'Failed to resume the agent');
      });
    },
    [sendSeedResume, handleSelectSession, showError],
  );

  const handleHandoverSeed = useCallback(
    (options: Parameters<typeof sendSeedHandover>[0]) =>
      sendSeedHandover({ ...options, sourceSessionId: contextSessionId || undefined }).then(
        (result) => {
          handleSelectSession(result.session_id);
          return result;
        },
      ),
    [contextSessionId, handleSelectSession, sendSeedHandover],
  );

  const handleSendSeedToChief = useCallback(
    (options: Parameters<typeof sendSeedToChief>[0]) =>
      sendSeedToChief({ ...options, sourceSessionId: contextSessionId || undefined }),
    [contextSessionId, sendSeedToChief],
  );

  const handleStartCrewMember = useCallback(
    (member: string) => {
      sendCrewStart(member)
        .then((result) => handleSelectSession(result.sessionId))
        .catch((error) =>
          showError(
            error instanceof Error ? error.message : `Failed to start ${crewDisplayName(member)}`,
          ),
        );
    },
    [sendCrewStart, handleSelectSession, showError],
  );

  const handleCloseCrewMember = useCallback(
    (member: string) => {
      sendCrewClose(member).catch((error) =>
        showError(
          error instanceof Error
            ? error.message
            : `Failed to ask ${crewDisplayName(member)} to close`,
        ),
      );
    },
    [sendCrewClose, showError],
  );

  return {
    handleStartCrewMember,
    handleCloseCrewMember,
    handleOpenSeedTile,
    handleOpenSeedFromCrew,
    handleRevealSeedInGarden,
    handleOpenMarkdownArtifact,
    checkArtifactPath,
    handleResumeSeed,
    handleHandoverSeed,
    handleSendSeedToChief,
  };
}
