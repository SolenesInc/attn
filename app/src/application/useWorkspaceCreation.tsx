import type { Dispatch, SetStateAction } from 'react';
import { useCallback, useEffect } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { ptySpawn } from '../pty/bridge';
import { useSessionStore } from '../store/sessions';
import { type SessionAgent } from '../types/sessionAgent';
import type { SessionCreationJob } from './appSupport';
import { AppContentProps, paneIdForSession } from './appSupport';
interface Options {
  sendWorkspaceClosePane: ReturnType<typeof useDaemonApi>['sendWorkspaceClosePane'];
  closeSession: ReturnType<typeof useSessionStore.getState>['closeSession'];
  sendUnregisterWorkspace: ReturnType<typeof useDaemonApi>['sendUnregisterWorkspace'];
  sendRegisterWorkspace: ReturnType<typeof useDaemonApi>['sendRegisterWorkspace'];
  createSession: ReturnType<typeof useSessionStore.getState>['createSession'];
  takeSessionSpawnArgs: ReturnType<typeof useSessionStore.getState>['takeSessionSpawnArgs'];
  sendWorkspaceAddSessionPane: ReturnType<typeof useDaemonApi>['sendWorkspaceAddSessionPane'];
  selectCreatedSession: (sessionId: string) => boolean;
  sessionCreationJob: SessionCreationJob | null;
  daemonSessions: AppContentProps['daemonSessions'];
  setSessionCreationJob: Dispatch<SetStateAction<SessionCreationJob | null>>;
}
export function useWorkspaceCreation({
  sendWorkspaceClosePane,
  closeSession,
  sendUnregisterWorkspace,
  sendRegisterWorkspace,
  createSession,
  takeSessionSpawnArgs,
  sendWorkspaceAddSessionPane,
  selectCreatedSession,
  sessionCreationJob,
  daemonSessions,
  setSessionCreationJob,
}: Options) {
  const rollbackSessionCreation = useCallback(
    async ({
      sessionId,
      workspaceId,
      paneId,
      unregisterWorkspace,
    }: {
      sessionId: string;
      workspaceId: string;
      paneId?: string;
      unregisterWorkspace?: boolean;
    }) => {
      if (paneId) {
        await sendWorkspaceClosePane(workspaceId, paneId).catch((error) => {
          console.error('[App] Failed to rollback workspace pane:', error);
        });
      }
      closeSession(sessionId);
      if (unregisterWorkspace) {
        await sendUnregisterWorkspace(workspaceId).catch((error) => {
          console.error('[App] Failed to rollback workspace:', error);
        });
      }
    },
    [closeSession, sendUnregisterWorkspace, sendWorkspaceClosePane],
  );

  const createWorkspaceSession = useCallback(
    async (
      label: string,
      cwd: string,
      providedSessionId?: string,
      agent?: SessionAgent,
      endpointId?: string,
      yoloMode = false,
      options?: { chiefOfStaff?: boolean; autoMode?: boolean },
    ) => {
      const sessionId = providedSessionId || crypto.randomUUID();
      const workspaceId = `workspace-${sessionId}`;
      const paneId = paneIdForSession(sessionId);
      let localCreated = false;
      let paneAdded = false;
      try {
        await sendRegisterWorkspace(workspaceId, label, cwd, endpointId);
        const createdSessionId = await createSession(
          label,
          cwd,
          sessionId,
          agent,
          endpointId,
          yoloMode,
          workspaceId,
          options?.chiefOfStaff,
          options?.autoMode,
        );
        localCreated = true;
        const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
        if (!spawnArgs) {
          throw new Error('Session spawn arguments were not prepared.');
        }
        await sendWorkspaceAddSessionPane(workspaceId, sessionId, label, { paneId });
        paneAdded = true;
        await ptySpawn({ args: spawnArgs });
        return createdSessionId;
      } catch (error) {
        if (localCreated) {
          await rollbackSessionCreation({
            sessionId,
            workspaceId,
            paneId: paneAdded ? paneId : undefined,
            unregisterWorkspace: true,
          });
        } else {
          await sendUnregisterWorkspace(workspaceId).catch(console.error);
        }
        throw error;
      }
    },
    [
      createSession,
      rollbackSessionCreation,
      sendRegisterWorkspace,
      sendWorkspaceAddSessionPane,
      sendUnregisterWorkspace,
      takeSessionSpawnArgs,
    ],
  );

  const createSessionForUiAutomation = useCallback(
    async (...args: Parameters<typeof createWorkspaceSession>) => {
      const sessionId = await createWorkspaceSession(...args);
      selectCreatedSession(sessionId);
      return sessionId;
    },
    [createWorkspaceSession, selectCreatedSession],
  );

  useEffect(() => {
    if (!sessionCreationJob?.sessionId || sessionCreationJob.error) {
      return;
    }
    if (daemonSessions.some((session) => session.id === sessionCreationJob.sessionId)) {
      selectCreatedSession(sessionCreationJob.sessionId);
      setSessionCreationJob((current) => (current?.id === sessionCreationJob.id ? null : current));
      return;
    }
    const timeoutId = window.setTimeout(() => {
      setSessionCreationJob((current) =>
        current?.id === sessionCreationJob.id
          ? { ...current, error: 'Session startup timed out.' }
          : current,
      );
    }, 35_000);
    return () => window.clearTimeout(timeoutId);
  }, [daemonSessions, selectCreatedSession, sessionCreationJob, setSessionCreationJob]);

  return { createWorkspaceSession, rollbackSessionCreation, createSessionForUiAutomation };
}
