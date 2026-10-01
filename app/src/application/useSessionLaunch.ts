import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { ptySpawn } from '../pty/bridge';
import { currentDesktopIn, useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import { type TerminalSplitDirection } from '../types/desktop';
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

interface Options {
  settings: AppContentProps['settings'];
  daemonEndpoints: AppContentProps['daemonEndpoints'];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  shownAgentId: string | null;
  selectCreatedSession: (id: string, focusOwner?: Element | null) => boolean;
  showError: ReturnType<typeof useToast>['showError'];
}

function localDesktopForLaunch(endpointId: string | undefined) {
  if (endpointId) {
    throw new Error('Agents on remote endpoints cannot be started or split in this release: remote endpoints are off.');
  }
  const desktop = currentDesktopIn(useProfilesStore.getState());
  if (!desktop) {
    throw new Error('No desktop is open yet; choose a profile before starting an agent.');
  }
  return desktop;
}
export function useSessionLaunch({
  settings,
  daemonEndpoints,
  sessions,
  shownAgentId,
  selectCreatedSession,
  showError,
}: Options) {
  const { sendCreateWorktree } = useDaemonApi();
  const { closeSession, createSession, takeSessionSpawnArgs } = useSessionStore();
  const activeLocalSession = sessions.find((session) => session.id === shownAgentId) ?? null;
  const currentDesktop = useProfilesStore(
    (state) => state.desktops.find((desktop) => desktop.id === state.currentDesktopId) ?? null,
  );
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = hasAnyAvailableAgents(agentAvailability);
  const [sessionCreationJob, setSessionCreationJob] = useState<SessionCreationJob | null>(null);
  const sessionCreationJobIdRef = useRef(0);
  const worktreeSessionCreateEndpointsRef = useRef<Set<string>>(new Set());

  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const [locationPickerPurpose, setLocationPickerPurpose] =
    useState<LocationPickerPurpose>('session');
  const locationPickerSessionDirection = useRef<TerminalSplitDirection>('vertical');
  const reopenPickRef = useRef<{ settle: (path?: string) => void } | null>(null);

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

  // The empty desktop's inline launcher is the new-session surface while it shows.
  const inlineLauncherFocusRef = useRef<(() => void) | null>(null);
  const registerInlineLauncher = useCallback((focus: (() => void) | null) => {
    inlineLauncherFocusRef.current = focus;
  }, []);

  const handleNewSession = useCallback((direction: TerminalSplitDirection = 'vertical') => {
    if (inlineLauncherFocusRef.current) {
      inlineLauncherFocusRef.current();
      return;
    }
    setLocationPickerPurpose('session');
    locationPickerSessionDirection.current = direction;
    setLocationPickerOpen(true);
  }, []);

  const spawnOnCurrentDesktop = useCallback(
    async (spawn: {
      sessionId: string;
      label: string;
      cwd: string;
      agent: SessionAgent;
      endpointId?: string;
      yoloMode?: boolean;
      autoMode?: boolean;
      chiefOfStaff?: boolean;
      direction: TerminalSplitDirection;
      anchorPaneId?: string;
      spawnedFrom?: string;
      desktopId?: string;
    }) => {
      // A slow pick (a worktree being created) lands on the desktop it was made from, if that still exists.
      const current = localDesktopForLaunch(spawn.endpointId);
      const desktop = useProfilesStore.getState().desktops.find((entry) => entry.id === spawn.desktopId) ?? current;
      const target = launchTarget(desktop, spawn.direction, spawn.anchorPaneId);
      const launch = useSessionStore.getState().beginIntent({ kind: 'session', sessionId: spawn.sessionId });
      let placementError: string | undefined;
      try {
        await createSession(
          spawn.label,
          spawn.cwd,
          spawn.sessionId,
          spawn.agent,
          spawn.endpointId,
          spawn.agent === 'shell' ? false : spawn.yoloMode,
          spawn.chiefOfStaff,
          spawn.agent === 'shell' ? undefined : spawn.autoMode,
        );
        const spawnArgs = takeSessionSpawnArgs(spawn.sessionId, 80, 24);
        if (!spawnArgs) {
          throw new Error('Session spawn arguments were not prepared.');
        }
        ({ placementError } = await ptySpawn({
          args: {
            ...spawnArgs,
            placement: target.placement,
            ...(spawn.spawnedFrom ? { spawned_from: spawn.spawnedFrom } : {}),
          },
        }));
      } catch (error) {
        useSessionStore.getState().intentFailed(launch);
        closeSession(spawn.sessionId);
        throw error;
      }
      const { intent } = useSessionStore.getState();
      if (intent?.id === launch) {
        selectCreatedSession(spawn.sessionId, intent.focusOwner);
      } else if (placementError) {
        showError(`${spawn.label} started without a pane on this desktop: ${placementError}`);
      }
      return spawn.sessionId;
    },
    [closeSession, createSession, selectCreatedSession, showError, takeSessionSpawnArgs],
  );

  const launchAgent = useCallback(
    (
      label: string,
      cwd: string,
      providedSessionId?: string,
      agent: SessionAgent = 'claude',
      endpointId?: string,
      yoloMode = false,
      options?: { chiefOfStaff?: boolean; autoMode?: boolean; desktopId?: string },
    ) =>
      spawnOnCurrentDesktop({
        sessionId: providedSessionId || crypto.randomUUID(),
        label,
        cwd,
        agent,
        endpointId,
        yoloMode,
        autoMode: options?.autoMode,
        chiefOfStaff: options?.chiefOfStaff,
        direction: 'vertical',
        desktopId: options?.desktopId,
      }),
    [spawnOnCurrentDesktop],
  );

  const createSessionForUiAutomation = launchAgent;

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
      const focusedSessionId = launchTarget(currentDesktop, direction, targetPaneId).focusedSessionId;
      const baseId = options.baseSessionId ?? activeLocalSession?.id ?? focusedSessionId;
      const base = sessions.find((session) => session.id === baseId) ?? null;
      const cwd = options.cwd || base?.cwd;
      if (!cwd) {
        handleNewSession(direction);
        return;
      }
      try {
        await spawnOnCurrentDesktop({
          sessionId: crypto.randomUUID(),
          label: options.label || nextSplitSessionLabel(agent),
          cwd,
          agent,
          endpointId: options.endpointId === null ? undefined : (options.endpointId ?? base?.endpointId),
          yoloMode: options.yoloMode ?? base?.yoloMode,
          autoMode: options.autoMode,
          direction,
          anchorPaneId: targetPaneId,
          spawnedFrom: base?.id,
          desktopId: options.desktopId,
        });
      } catch (error) {
        showError(error instanceof Error ? error.message : 'Failed to start the agent');
      }
    },
    [
      activeLocalSession,
      currentDesktop,
      handleNewSession,
      nextSplitSessionLabel,
      sessions,
      showError,
      spawnOnCurrentDesktop,
    ],
  );

  const launchPicked = useCallback(
    async (pick: {
      label: string;
      cwd: string;
      agent: SessionAgent;
      endpointId?: string;
      yoloMode: boolean;
      autoMode?: boolean;
      chiefOfStaff: boolean;
      desktopId?: string;
    }): Promise<string | null> => {
      if (!pick.chiefOfStaff) {
        await createSplitSession(pick.agent, locationPickerSessionDirection.current, undefined, {
          cwd: pick.cwd,
          endpointId: pick.endpointId ?? null,
          label: pick.label,
          yoloMode: pick.yoloMode,
          autoMode: pick.autoMode,
          desktopId: pick.desktopId,
        });
        return null;
      }
      const sessionId = await launchAgent(
        pick.label,
        pick.cwd,
        undefined,
        pick.agent,
        pick.endpointId,
        pick.yoloMode,
        { chiefOfStaff: true, autoMode: pick.autoMode, desktopId: pick.desktopId },
      );
      return sessionId;
    },
    [createSplitSession, launchAgent],
  );

  const launchLocation = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      endpointId?: string,
      yoloMode = false,
      chiefOfStaff = false,
      autoMode?: boolean,
    ) => {
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
          showError(endpoint.status_message || `Endpoint ${endpoint.name} is ${endpoint.status}.`);
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
      const pick = { label: folderName, cwd: path, agent: selectedAgent, endpointId, yoloMode, autoMode, chiefOfStaff };
      if (!chiefOfStaff) {
        await launchPicked(pick);
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
        await launchPicked(pick);
        setSessionCreationJob((current) => (current?.id === jobId ? null : current));
      } catch (err) {
        setSessionCreationJob((current) =>
          current?.id === jobId
            ? { ...current, error: err instanceof Error ? err.message : 'Failed to create session' }
            : current,
        );
      }
    },
    [agentAvailability, daemonEndpoints, hasAvailableAgents, launchPicked, showError],
  );

  const handleLocationSelect = useCallback(
    async (...pick: Parameters<typeof launchLocation>) => {
      if (locationPickerPurpose === 'reopen') {
        reopenPickRef.current?.settle(pick[0]);
        reopenPickRef.current = null;
        return;
      }
      await launchLocation(...pick);
    },
    [launchLocation, locationPickerPurpose],
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
      chiefOfStaff = false,
    ) => {
      let desktopId: string;
      try {
        desktopId = localDesktopForLaunch(endpointId).id;
      } catch (error) {
        showError(error instanceof Error ? error.message : 'Failed to start the agent');
        return;
      }
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
          await launchPicked({
            label: folderName,
            cwd: worktreePath,
            agent,
            endpointId,
            yoloMode,
            autoMode,
            chiefOfStaff,
            desktopId,
          });
          setSessionCreationJob((current) => (current?.id === jobId ? null : current));
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
    [launchPicked, sendCreateWorktree, showError],
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
    launchAgent,
    createSessionForUiAutomation,
    locationPickerOpen,
    locationPickerPurpose,
    closeLocationPicker,
    handleLocationSelect,
    launchLocation,
    registerInlineLauncher,
    handleCreateWorktreeSession,
    handleNewSession,
    createSplitSession,
    chooseReopenDirectory,
  };
}
