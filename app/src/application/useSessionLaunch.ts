import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useErrorToast } from '../components/ErrorToast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useSessionWorkspaceController } from '../hooks/useSessionWorkspaceController';
import { ptySpawn } from '../pty/bridge';
import { useSessionStore } from '../store/sessions';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import { type TerminalSplitDirection } from '../types/workspace';
import {
  agentLabel,
  getAgentAvailability,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import {
  AppContentProps,
  LocationPickerPurpose,
  paneIdForSession,
  SessionCreationJob,
  SplitSessionOptions,
  TERMINAL_AGENT,
} from './appSupport';
import { useWorkspaceCreation } from './useWorkspaceCreation';

interface Options {
  settings: AppContentProps['settings'];
  daemonSessions: AppContentProps['daemonSessions'];
  daemonEndpoints: AppContentProps['daemonEndpoints'];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  activeSessionId: string | null;
  getActivePaneIdForSession: ReturnType<
    typeof useSessionWorkspaceController
  >['getActivePaneIdForSession'];
  selectCreatedSession: (id: string) => boolean;
  showError: ReturnType<typeof useErrorToast>['showError'];
}
export function useSessionLaunch({
  settings,
  daemonSessions,
  daemonEndpoints,
  sessions,
  activeSessionId,
  getActivePaneIdForSession,
  selectCreatedSession,
  showError,
}: Options) {
  const {
    sendWorkspaceClosePane,
    sendUnregisterWorkspace,
    sendRegisterWorkspace,
    sendWorkspaceAddSessionPane,
    sendCreateWorktree,
  } = useDaemonApi();
  const { closeSession, createSession, takeSessionSpawnArgs } = useSessionStore();
  const activeLocalSession = sessions.find((session) => session.id === activeSessionId) ?? null;
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = hasAnyAvailableAgents(agentAvailability);
  const [sessionCreationJob, setSessionCreationJob] = useState<SessionCreationJob | null>(null);
  const sessionCreationJobIdRef = useRef(0);
  const worktreeSessionCreateEndpointsRef = useRef<Set<string>>(new Set());
  const { createWorkspaceSession, rollbackSessionCreation, createSessionForUiAutomation } =
    useWorkspaceCreation({
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
    });

  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const [locationPickerPurpose, setLocationPickerPurpose] =
    useState<LocationPickerPurpose>('workspace');
  const locationPickerSessionDirection = useRef<TerminalSplitDirection>('vertical');
  const reopenPickRef = useRef<{ settle: (path?: string) => void } | null>(null);

  const handleNewWorkspace = useCallback(() => {
    setLocationPickerPurpose('workspace');
    setLocationPickerOpen(true);
  }, []);

  const nextSplitSessionLabel = useCallback(
    (workspaceId: string, agent: SessionAgent) => {
      const normalizedAgent = normalizeSessionAgent(agent, 'codex');
      const base = normalizedAgent === 'shell' ? 'shell' : normalizedAgent;
      const matchingCount = sessions.filter(
        (session) =>
          session.workspaceId === workspaceId &&
          normalizeSessionAgent(session.agent, 'codex') === normalizedAgent,
      ).length;
      return matchingCount === 0 ? base : `${base} ${matchingCount + 1}`;
    },
    [sessions],
  );

  const createSplitSession = useCallback(
    async (
      agent: SessionAgent,
      direction: 'vertical' | 'horizontal',
      targetPaneId?: string,
      options: SplitSessionOptions = {},
    ) => {
      const activeSession = options.baseSessionId
        ? sessions.find((session) => session.id === options.baseSessionId)
        : activeLocalSession;
      if (!activeSession?.workspaceId) {
        handleNewWorkspace();
        return;
      }
      const sessionId = crypto.randomUUID();
      const workspaceId = activeSession.workspaceId;
      const paneId = targetPaneId || getActivePaneIdForSession(activeSession);
      const newPaneId = paneIdForSession(sessionId);
      const label = options.label || nextSplitSessionLabel(workspaceId, agent);
      const endpointId =
        options.endpointId === null ? undefined : (options.endpointId ?? activeSession.endpointId);
      let paneAdded = false;

      try {
        await createSession(
          label,
          options.cwd || activeSession.cwd,
          sessionId,
          agent,
          endpointId,
          agent === 'shell' ? false : (options.yoloMode ?? activeSession.yoloMode),
          workspaceId,
          undefined,
          agent === 'shell' ? undefined : options.autoMode,
        );
        const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
        await sendWorkspaceAddSessionPane(workspaceId, sessionId, label, {
          paneId: newPaneId,
          targetPaneId: paneId,
          direction,
        });
        paneAdded = true;
        if (spawnArgs) {
          await ptySpawn({ args: { ...spawnArgs, spawned_from: activeSession.id } });
        } else {
          throw new Error('Session spawn arguments were not prepared.');
        }
        selectCreatedSession(sessionId);
      } catch (error) {
        await rollbackSessionCreation({
          sessionId,
          workspaceId,
          paneId: paneAdded ? newPaneId : undefined,
        });
        showError(error instanceof Error ? error.message : 'Failed to create session split');
      }
    },
    [
      activeLocalSession,
      createSession,
      getActivePaneIdForSession,
      handleNewWorkspace,
      nextSplitSessionLabel,
      rollbackSessionCreation,
      sendWorkspaceAddSessionPane,
      sessions,
      selectCreatedSession,
      showError,
      takeSessionSpawnArgs,
    ],
  );

  const handleNewSession = useCallback(
    (direction: TerminalSplitDirection = 'vertical') => {
      if (!activeLocalSession?.workspaceId) {
        handleNewWorkspace();
        return;
      }
      setLocationPickerPurpose('session');
      locationPickerSessionDirection.current = direction;
      setLocationPickerOpen(true);
    },
    [activeLocalSession?.workspaceId, handleNewWorkspace],
  );

  const handleLocationSelect = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      endpointId?: string,
      yoloMode = false,
      chiefOfStaff = false,
      autoMode?: boolean,
    ) => {
      if (locationPickerPurpose === 'reopen') {
        reopenPickRef.current?.settle(path);
        reopenPickRef.current = null;
        return;
      }
      const jobId = sessionCreationJobIdRef.current + 1;
      sessionCreationJobIdRef.current = jobId;
      let selectedAgent: SessionAgent;
      if (endpointId) {
        const endpoint = daemonEndpoints.find((entry) => entry.id === endpointId);
        if (!endpoint) {
          showError('Selected endpoint no longer exists.');
          return;
        }
        if (endpoint.status !== 'connected') {
          showError(`Endpoint ${endpoint.name} is ${endpoint.status}.`);
          return;
        }
        if (agent !== TERMINAL_AGENT && !endpoint.capabilities?.agents_available.includes(agent)) {
          showError(`${agentLabel(agent)} is not available on ${endpoint.name}.`);
          return;
        }
        selectedAgent = agent;
      } else {
        if (agent !== TERMINAL_AGENT && !hasAvailableAgents) {
          showError('No supported agent CLI found in PATH.');
          return;
        }
        selectedAgent =
          agent === TERMINAL_AGENT
            ? TERMINAL_AGENT
            : resolvePreferredAgent(agent, agentAvailability, 'codex');
      }
      const folderName = path.split('/').pop() || 'session';
      if (locationPickerPurpose === 'session' && activeLocalSession?.workspaceId) {
        await createSplitSession(selectedAgent, locationPickerSessionDirection.current, undefined, {
          cwd: path,
          endpointId: endpointId ?? null,
          label: folderName,
          yoloMode,
          autoMode,
        });
        return;
      }
      setSessionCreationJob({
        id: jobId,
        label: folderName,
        path,
        phase: 'starting_session',
        error: null,
      });
      try {
        const sessionId = await createWorkspaceSession(
          folderName,
          path,
          undefined,
          selectedAgent,
          endpointId,
          yoloMode,
          { chiefOfStaff, autoMode },
        );
        selectCreatedSession(sessionId);
        setSessionCreationJob((current) =>
          current?.id === jobId ? { ...current, sessionId, phase: 'starting_session' } : current,
        );
      } catch (err) {
        setSessionCreationJob((current) =>
          current?.id === jobId
            ? { ...current, error: err instanceof Error ? err.message : 'Failed to create session' }
            : current,
        );
      }
    },
    [
      activeLocalSession?.workspaceId,
      agentAvailability,
      createSplitSession,
      createWorkspaceSession,
      daemonEndpoints,
      hasAvailableAgents,
      locationPickerPurpose,
      selectCreatedSession,
      showError,
    ],
  );

  const handleCreateWorktreeSession = useCallback(
    (
      mainRepo: string,
      branchName: string,
      startingFrom: string,
      endpointId: string | undefined,
      agent: SessionAgent,
      yoloMode: boolean,
      autoMode?: boolean,
    ) => {
      const endpointKey = endpointId || 'local';
      if (worktreeSessionCreateEndpointsRef.current.has(endpointKey)) {
        showError('A worktree session is already being created for this target.');
        return;
      }
      worktreeSessionCreateEndpointsRef.current.add(endpointKey);
      const jobId = sessionCreationJobIdRef.current + 1;
      sessionCreationJobIdRef.current = jobId;
      setSessionCreationJob({
        id: jobId,
        label: branchName,
        path: mainRepo,
        phase: 'creating_worktree',
        error: null,
      });

      void (async () => {
        try {
          const result = await sendCreateWorktree(
            mainRepo,
            branchName,
            undefined,
            startingFrom,
            endpointId,
          );
          if (!result.success || !result.path) {
            throw new Error(result.error || 'Failed to create worktree');
          }
          const worktreePath = result.path;
          setSessionCreationJob((current) =>
            current?.id === jobId
              ? { ...current, path: worktreePath, phase: 'starting_session' }
              : current,
          );
          const folderName = worktreePath.split('/').pop() || branchName || 'session';
          if (locationPickerPurpose === 'session' && activeLocalSession?.workspaceId) {
            await createSplitSession(agent, locationPickerSessionDirection.current, undefined, {
              cwd: worktreePath,
              endpointId: endpointId ?? null,
              label: folderName,
              yoloMode,
              autoMode,
            });
            setSessionCreationJob((current) => (current?.id === jobId ? null : current));
            return;
          }
          const sessionId = await createWorkspaceSession(
            folderName,
            worktreePath,
            undefined,
            agent,
            endpointId,
            yoloMode,
            { autoMode },
          );
          selectCreatedSession(sessionId);
          setSessionCreationJob((current) =>
            current?.id === jobId
              ? {
                  ...current,
                  label: folderName,
                  path: worktreePath,
                  phase: 'starting_session',
                  sessionId,
                }
              : current,
          );
        } catch (err) {
          setSessionCreationJob((current) =>
            current?.id === jobId
              ? {
                  ...current,
                  error: err instanceof Error ? err.message : 'Failed to create session',
                }
              : current,
          );
        } finally {
          worktreeSessionCreateEndpointsRef.current.delete(endpointKey);
        }
      })();
    },
    [
      activeLocalSession?.workspaceId,
      createSplitSession,
      createWorkspaceSession,
      locationPickerPurpose,
      selectCreatedSession,
      sendCreateWorktree,
      showError,
    ],
  );

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
    reopenPickRef.current?.settle(undefined);
    reopenPickRef.current = null;
  }, []);

  const chooseReopenDirectory = useCallback(
    () =>
      new Promise<string | undefined>((settle) => {
        reopenPickRef.current?.settle(undefined);
        reopenPickRef.current = { settle };
        setLocationPickerPurpose('reopen');
        setLocationPickerOpen(true);
      }),
    [],
  );
  useEffect(
    () => () => {
      reopenPickRef.current?.settle(undefined);
    },
    [],
  );

  return {
    sessionCreationJob,
    setSessionCreationJob,
    createWorkspaceSession,
    createSessionForUiAutomation,
    locationPickerOpen,
    locationPickerPurpose,
    closeLocationPicker,
    handleLocationSelect,
    handleCreateWorktreeSession,
    handleNewWorkspace,
    handleNewSession,
    createSplitSession,
    chooseReopenDirectory,
  };
}
