import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useErrorToast } from '../components/ErrorToast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { ptySpawn } from '../pty/bridge';
import { useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import { type TerminalSplitDirection } from '../types/workspace';
import {
  agentLabel,
  getAgentAvailability,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import { launchTarget } from '../utils/launchPlacement';
import {
  AppContentProps,
  LocationPickerPurpose,
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
  selectCreatedSession: (id: string) => boolean;
  showError: ReturnType<typeof useErrorToast>['showError'];
}
export function useSessionLaunch({
  settings,
  daemonSessions,
  daemonEndpoints,
  sessions,
  activeSessionId,
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
  const currentDesktop = useProfilesStore(
    (state) => state.desktops.find((desktop) => desktop.id === state.currentDesktopId) ?? null,
  );
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = hasAnyAvailableAgents(agentAvailability);
  const [sessionCreationJob, setSessionCreationJob] = useState<SessionCreationJob | null>(null);
  const sessionCreationJobIdRef = useRef(0);
  const worktreeSessionCreateEndpointsRef = useRef<Set<string>>(new Set());
  const { createWorkspaceSession, createSessionForUiAutomation } =
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
    (agent: SessionAgent) => {
      const normalizedAgent = normalizeSessionAgent(agent, 'codex');
      const base = normalizedAgent === 'shell' ? 'shell' : normalizedAgent;
      const onDesktop = new Set(currentDesktop?.panes.map((pane) => pane.session_id));
      const matchingCount = sessions.filter(
        (session) =>
          onDesktop.has(session.id) && normalizeSessionAgent(session.agent, 'codex') === normalizedAgent,
      ).length;
      return matchingCount === 0 ? base : `${base} ${matchingCount + 1}`;
    },
    [currentDesktop, sessions],
  );

  const handleNewSession = useCallback((direction: TerminalSplitDirection = 'vertical') => {
    setLocationPickerPurpose('session');
    locationPickerSessionDirection.current = direction;
    setLocationPickerOpen(true);
  }, []);

  const createSplitSession = useCallback(
    async (
      agent: SessionAgent,
      direction: 'vertical' | 'horizontal',
      targetPaneId?: string,
      options: SplitSessionOptions = {},
    ) => {
      if (!currentDesktop) {
        showError('No desktop is open yet; choose a profile before starting an agent.');
        return;
      }
      const target = launchTarget(currentDesktop, direction, targetPaneId);
      const baseId = options.baseSessionId ?? activeLocalSession?.id ?? target.focusedSessionId;
      const base = sessions.find((session) => session.id === baseId) ?? null;
      const cwd = options.cwd || base?.cwd;
      if (!cwd) {
        handleNewSession(direction);
        return;
      }
      const sessionId = crypto.randomUUID();
      const label = options.label || nextSplitSessionLabel(agent);
      const endpointId =
        options.endpointId === null ? undefined : (options.endpointId ?? base?.endpointId);
      try {
        await createSession(
          label,
          cwd,
          sessionId,
          agent,
          endpointId,
          agent === 'shell' ? false : (options.yoloMode ?? base?.yoloMode),
          undefined,
          undefined,
          agent === 'shell' ? undefined : options.autoMode,
        );
        const spawnArgs = takeSessionSpawnArgs(sessionId, 80, 24);
        if (!spawnArgs) {
          throw new Error('Session spawn arguments were not prepared.');
        }
        await ptySpawn({
          args: {
            ...spawnArgs,
            ...(endpointId ? {} : { placement: target.placement }),
            ...(base ? { spawned_from: base.id } : {}),
          },
        });
        selectCreatedSession(sessionId);
      } catch (error) {
        closeSession(sessionId);
        showError(error instanceof Error ? error.message : 'Failed to start the agent');
      }
    },
    [
      activeLocalSession,
      closeSession,
      createSession,
      currentDesktop,
      handleNewSession,
      nextSplitSessionLabel,
      sessions,
      selectCreatedSession,
      showError,
      takeSessionSpawnArgs,
    ],
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
      if (locationPickerPurpose === 'session') {
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
          if (locationPickerPurpose === 'session') {
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
