import { useCallback } from 'react';
import { useErrorToast } from '../components/ErrorToast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { type SeedReviewActionContext } from '../hooks/useDaemonSocket';
import { useDockPanels } from '../hooks/useDockPanels';
import { useSessionWorkspaceController } from '../hooks/useSessionWorkspaceController';
import { useDaemonStore } from '../store/daemonSessions';
import { gardenPathToSeed, useGardenWalk } from '../store/gardenWalk';
import { useSessionStore } from '../store/sessions';
import { crewDisplayName } from '../utils/crewName';
interface Options {
  sendOpenSeed: ReturnType<typeof useDaemonApi>['sendOpenSeed'];
  activeSessionId: ReturnType<typeof useSessionStore.getState>['activeSessionId'];
  showError: ReturnType<typeof useErrorToast>['showError'];
  seeds: ReturnType<typeof useDaemonStore.getState>['seeds'];
  openDockPanel: ReturnType<typeof useDockPanels>['openDockPanel'];
  sendFsExists: ReturnType<typeof useDaemonApi>['sendFsExists'];
  sendOpenMarkdown: ReturnType<typeof useDaemonApi>['sendOpenMarkdown'];
  sendSeedResume: ReturnType<typeof useDaemonApi>['sendSeedResume'];
  handleSelectSession: (sessionId: string) => boolean;
  sendSeedHandover: ReturnType<typeof useDaemonApi>['sendSeedHandover'];
  sendSeedToChief: ReturnType<typeof useDaemonApi>['sendSeedToChief'];
  sendCrewWake: ReturnType<typeof useDaemonApi>['sendCrewWake'];
  sendCrewSleep: ReturnType<typeof useDaemonApi>['sendCrewSleep'];
  handleSelectTile: (workspaceId: string, tileId: string) => void;
  focusWorkspaceLeaf: ReturnType<typeof useSessionWorkspaceController>['focusWorkspaceLeaf'];
  setCrewSeedTile: (tile: { workspaceId: string; tileId: string } | null) => void;
  closeCrewPanel: () => void;
}
export function useAppGardenActions({
  sendOpenSeed,
  activeSessionId,
  showError,
  seeds,
  openDockPanel,
  sendFsExists,
  sendOpenMarkdown,
  sendSeedResume,
  handleSelectSession,
  sendSeedHandover,
  sendSeedToChief,
  sendCrewWake,
  sendCrewSleep,
  handleSelectTile,
  focusWorkspaceLeaf,
  setCrewSeedTile,
  closeCrewPanel,
}: Options) {
  const openSeedTile = useCallback(
    async (
      seedId: string,
      beforeFocus?: (opened: { workspaceId: string; tileId: string }) => void,
      placementSessionId?: string,
    ) => {
      const opened = await sendOpenSeed(seedId, placementSessionId || activeSessionId || '');
      if (!opened.workspaceId || !opened.tileId) {
        throw new Error(`The daemon opened ${seedId} without a workspace tile`);
      }
      const { workspaceId, tileId } = opened;
      beforeFocus?.({ workspaceId, tileId });
      handleSelectTile(workspaceId, tileId);
      return opened;
    },
    [sendOpenSeed, activeSessionId, handleSelectTile],
  );

  const handleOpenSeedTile = useCallback(
    (seedId: string) => {
      void openSeedTile(seedId).catch((error) => {
        showError(error instanceof Error ? error.message : 'Could not open the seed');
      });
    },
    [openSeedTile, showError],
  );

  const handleOpenSeedFromCrew = useCallback(
    (seedId: string, placementSessionId?: string) => {
      void openSeedTile(
        seedId,
        (opened) => {
          setCrewSeedTile(opened);
          closeCrewPanel();
        },
        placementSessionId,
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
        showError(`Could not find ${seedId} in the Garden`);
        return;
      }
      useGardenWalk.getState().setTrail(trail);
      openDockPanel('garden');
    },
    [openDockPanel, seeds, showError],
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
      void sendOpenMarkdown(path, '')
        .then(({ workspaceId, tileId }) => {
          if (workspaceId && tileId) focusWorkspaceLeaf(workspaceId, tileId);
        })
        .catch((error) => {
          showError(error instanceof Error ? error.message : 'Could not open the document');
        });
    },
    [focusWorkspaceLeaf, sendOpenMarkdown, showError],
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
        throw error;
      });
    },
    [sendSeedResume, handleSelectSession, showError],
  );

  const handleHandoverSeed = useCallback(
    (options: Parameters<typeof sendSeedHandover>[0]) =>
      sendSeedHandover({ ...options, sourceSessionId: activeSessionId || undefined }).then(
        (result) => {
          handleSelectSession(result.session_id);
          return result;
        },
      ),
    [activeSessionId, handleSelectSession, sendSeedHandover],
  );

  const handleSendSeedToChief = useCallback(
    (options: Parameters<typeof sendSeedToChief>[0]) =>
      sendSeedToChief({ ...options, sourceSessionId: activeSessionId || undefined }),
    [activeSessionId, sendSeedToChief],
  );

  const handleWakeCrewMember = useCallback(
    (member: string) => {
      sendCrewWake(member)
        .then((result) => handleSelectSession(result.sessionId))
        .catch((error) =>
          showError(
            error instanceof Error ? error.message : `Failed to wake ${crewDisplayName(member)}`,
          ),
        );
    },
    [sendCrewWake, handleSelectSession, showError],
  );

  const handleSleepCrewMember = useCallback(
    (member: string) => {
      sendCrewSleep(member).catch((error) =>
        showError(
          error instanceof Error
            ? error.message
            : `Failed to ask ${crewDisplayName(member)} to sleep`,
        ),
      );
    },
    [sendCrewSleep, showError],
  );

  return {
    handleWakeCrewMember,
    handleSleepCrewMember,
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
