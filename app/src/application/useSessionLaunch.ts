import { useCallback, useMemo, useRef, useState } from 'react';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { ptySpawn } from '../pty/bridge';
import { currentDesktopIn, useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import { type TerminalSplitDirection } from '../types/desktop';
import {
  getAgentAvailability,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import { launchTarget } from '../utils/launchPlacement';
import {
  AppContentProps,
  SessionCreationJob,
  SplitSessionOptions,
  TERMINAL_AGENT,
} from './appSupport';

interface Options {
  settings: AppContentProps['settings'];
  sessions: ReturnType<typeof useSessionStore.getState>['sessions'];
  shownAgentId: string | null;
  selectCreatedSession: (id: string, focusOwner?: Element | null) => boolean;
  showError: ReturnType<typeof useToast>['showError'];
}

function currentDesktopForLaunch() {
  const desktop = currentDesktopIn(useProfilesStore.getState());
  if (!desktop) {
    throw new Error('No desktop is open yet; choose a profile before starting an agent.');
  }
  return desktop;
}
export function useSessionLaunch({
  settings,
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
  const worktreeSessionCreatingRef = useRef(false);

  const [locationPickerInitialAgent, setLocationPickerInitialAgent] = useState<
    SessionAgent | undefined
  >();
  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const locationPickerSessionDirection = useRef<TerminalSplitDirection>('vertical');

  const nextSplitSessionLabel = useCallback(
    (agent: SessionAgent) => {
      const normalizedAgent = normalizeSessionAgent(agent, 'codex');
      const base = normalizedAgent === 'shell' ? 'shell' : normalizedAgent;
      const onDesktop = new Set(currentDesktop?.panes.map((pane) => pane.session_id));
      const matchingCount = sessions.filter(
        (session) =>
          onDesktop.has(session.id) &&
          normalizeSessionAgent(session.agent, 'codex') === normalizedAgent,
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

  const handleNewSession = useCallback(
    (direction: TerminalSplitDirection = 'vertical', agent?: SessionAgent) => {
      if (inlineLauncherFocusRef.current && !agent) {
        inlineLauncherFocusRef.current();
        return;
      }
      setLocationPickerInitialAgent(agent);
      locationPickerSessionDirection.current = direction;
      setLocationPickerOpen(true);
    },
    [],
  );

  const spawnOnCurrentDesktop = useCallback(
    async (spawn: {
      sessionId: string;
      label: string;
      cwd: string;
      agent: SessionAgent;
      yoloMode?: boolean;
      autoMode?: boolean;
      chiefOfStaff?: boolean;
      direction: TerminalSplitDirection;
      anchorPaneId?: string;
      spawnedFrom?: string;
      desktopId?: string;
    }) => {
      // A pick lands on the desktop it was made for, even when it answers after the user moved on;
      // then it starts in the background instead of pulling the user back.
      const current = currentDesktopForLaunch();
      const desktop =
        useProfilesStore.getState().desktops.find((entry) => entry.id === spawn.desktopId) ??
        current;
      const target = launchTarget(desktop, spawn.direction, spawn.anchorPaneId);
      const launch =
        desktop.id === current.id
          ? useSessionStore.getState().beginIntent({ kind: 'session', sessionId: spawn.sessionId })
          : null;
      try {
        await createSession(
          spawn.label,
          spawn.cwd,
          spawn.sessionId,
          spawn.agent,
          spawn.agent === 'shell' ? false : spawn.yoloMode,
          spawn.chiefOfStaff,
          spawn.agent === 'shell' ? undefined : spawn.autoMode,
        );
        const spawnArgs = takeSessionSpawnArgs(spawn.sessionId, 80, 24);
        if (!spawnArgs) {
          throw new Error('Session spawn arguments were not prepared.');
        }
        await ptySpawn({
          args: {
            ...spawnArgs,
            placement: target.placement,
            ...(spawn.spawnedFrom ? { spawned_from: spawn.spawnedFrom } : {}),
          },
        });
      } catch (error) {
        if (launch) useSessionStore.getState().intentFailed(launch);
        closeSession(spawn.sessionId);
        throw error;
      }
      const { intent } = useSessionStore.getState();
      if (launch && intent?.id === launch) {
        selectCreatedSession(spawn.sessionId, intent.focusOwner);
      }
      return spawn.sessionId;
    },
    [closeSession, createSession, selectCreatedSession, takeSessionSpawnArgs],
  );

  const launchAgent = useCallback(
    (
      label: string,
      cwd: string,
      providedSessionId?: string,
      agent: SessionAgent = 'claude',
      yoloMode = false,
      options?: { chiefOfStaff?: boolean; autoMode?: boolean; desktopId?: string },
    ) =>
      spawnOnCurrentDesktop({
        sessionId: providedSessionId || crypto.randomUUID(),
        label,
        cwd,
        agent,
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
      const focusedSessionId = launchTarget(
        currentDesktop,
        direction,
        targetPaneId,
      ).focusedSessionId;
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
      yoloMode: boolean;
      autoMode?: boolean;
      chiefOfStaff: boolean;
      desktopId?: string;
    }): Promise<string | null> => {
      if (!pick.chiefOfStaff) {
        const desktop = currentDesktopForLaunch();
        const direction = locationPickerSessionDirection.current;
        const baseId = activeLocalSession?.id ?? launchTarget(desktop, direction).focusedSessionId;
        return await spawnOnCurrentDesktop({
          sessionId: crypto.randomUUID(),
          label: pick.label,
          cwd: pick.cwd,
          agent: pick.agent,
          yoloMode: pick.yoloMode,
          autoMode: pick.autoMode,
          chiefOfStaff: false,
          direction,
          spawnedFrom: baseId || undefined,
          desktopId: pick.desktopId,
        });
      }
      const sessionId = await launchAgent(
        pick.label,
        pick.cwd,
        undefined,
        pick.agent,
        pick.yoloMode,
        { chiefOfStaff: true, autoMode: pick.autoMode, desktopId: pick.desktopId },
      );
      return sessionId;
    },
    [activeLocalSession, spawnOnCurrentDesktop, launchAgent],
  );

  const startLocation = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      yoloMode = false,
      chiefOfStaff = false,
      autoMode?: boolean,
      desktopId?: string,
    ) => {
      if (agent !== TERMINAL_AGENT && !hasAvailableAgents)
        throw new Error('No supported agent CLI found in PATH.');
      const selectedAgent =
        agent === TERMINAL_AGENT ? agent : resolvePreferredAgent(agent, agentAvailability, 'codex');
      return launchPicked({
        label: path.split('/').pop() || 'session',
        cwd: path,
        agent: selectedAgent,
        yoloMode,
        autoMode,
        chiefOfStaff,
        desktopId,
      });
    },
    [agentAvailability, hasAvailableAgents, launchPicked],
  );

  const launchLocation = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      yoloMode = false,
      chiefOfStaff = false,
      autoMode?: boolean,
      desktopId?: string,
    ) => {
      const jobId = sessionCreationJobIdRef.current + 1;
      sessionCreationJobIdRef.current = jobId;
      if (agent !== TERMINAL_AGENT && !hasAvailableAgents) {
        showError('No supported agent CLI found in PATH.');
        return;
      }
      const selectedAgent =
        agent === TERMINAL_AGENT
          ? TERMINAL_AGENT
          : resolvePreferredAgent(agent, agentAvailability, 'codex');
      const folderName = path.split('/').pop() || 'session';
      const pick = {
        label: folderName,
        cwd: path,
        agent: selectedAgent,
        yoloMode,
        autoMode,
        chiefOfStaff,
        desktopId,
      };
      if (!chiefOfStaff) {
        try {
          await launchPicked(pick);
        } catch (error) {
          showError(error instanceof Error ? error.message : 'Failed to start the agent');
        }
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
    [agentAvailability, hasAvailableAgents, launchPicked, showError],
  );

  const startWorktreeSession = useCallback(
    async (
      mainRepo: string,
      branch: string,
      startingFrom: string,
      agent: SessionAgent,
      yoloMode: boolean,
      autoMode?: boolean,
      chiefOfStaff = false,
      desktopId?: string,
      onCreated?: (path: string) => void,
    ) => {
      const pickedDesktopId = desktopId ?? currentDesktopForLaunch().id;
      const result = await sendCreateWorktree(mainRepo, branch, undefined, startingFrom);
      if (!result.success || !result.path)
        throw new Error(result.error || 'Failed to create worktree');
      onCreated?.(result.path);
      return startLocation(result.path, agent, yoloMode, chiefOfStaff, autoMode, pickedDesktopId);
    },
    [sendCreateWorktree, startLocation],
  );

  const handleCreateWorktreeSession = useCallback(
    async (
      mainRepo: string,
      branchName: string,
      startingFrom: string,
      agent: SessionAgent,
      yoloMode: boolean,
      autoMode?: boolean,
      chiefOfStaff = false,
      pickedOnDesktopId?: string,
    ) => {
      if (worktreeSessionCreatingRef.current) {
        showError('A worktree session is already being created.');
        return;
      }
      worktreeSessionCreatingRef.current = true;
      const jobId = ++sessionCreationJobIdRef.current;
      setSessionCreationJob({
        id: jobId,
        label: branchName,
        path: mainRepo,
        phase: 'creating_worktree',
        error: null,
      });
      try {
        await startWorktreeSession(
          mainRepo,
          branchName,
          startingFrom,
          agent,
          yoloMode,
          autoMode,
          chiefOfStaff,
          pickedOnDesktopId,
          (path) => {
            setSessionCreationJob((current) =>
              current?.id === jobId ? { ...current, path, phase: 'starting_session' } : current,
            );
          },
        );
        setSessionCreationJob((current) => (current?.id === jobId ? null : current));
      } catch (error) {
        setSessionCreationJob((current) =>
          current?.id === jobId
            ? {
                ...current,
                error: error instanceof Error ? error.message : 'Failed to create session',
              }
            : current,
        );
      } finally {
        worktreeSessionCreatingRef.current = false;
      }
    },
    [startWorktreeSession, showError],
  );

  const closeLocationPicker = useCallback(() => {
    setLocationPickerOpen(false);
  }, []);

  return {
    sessionCreationJob,
    setSessionCreationJob,
    launchAgent,
    createSessionForUiAutomation,
    locationPickerOpen,
    locationPickerInitialAgent,
    startLocation,
    startWorktreeSession,
    closeLocationPicker,
    launchLocation,
    registerInlineLauncher,
    handleCreateWorktreeSession,
    handleNewSession,
    createSplitSession,
  };
}
