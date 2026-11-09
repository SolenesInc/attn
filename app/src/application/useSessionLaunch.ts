import { useCallback, useMemo, useRef, useState } from 'react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { useToast } from '../components/Toast';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { ptySpawn } from '../pty/bridge';
import { stackLimitMessage } from '../components/SessionTerminalDesktop/rowLayout';
import { stackRoomPx } from '../store/desktopShelf';
import { currentDesktopIn, useProfilesStore } from '../store/profiles';
import { useSessionStore } from '../store/sessions';
import { normalizeSessionAgent, type SessionAgent } from '../types/sessionAgent';
import { leafSlotId, parseLayoutJSON, poppedLeaves, type TerminalSplitDirection } from '../types/desktop';
import type { Desktop } from '../types/generated';
import {
  getAgentAvailability,
  hasAnySelectableAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import { launchTarget } from '../utils/launchPlacement';
import type { ShortcutSource } from '../shortcuts/useShortcut';
import {
  AppContentProps,
  SessionCreationJob,
  SplitSessionOptions,
  TERMINAL_AGENT,
} from './appSupport';

interface Options {
  settings: AppContentProps['settings'];
  sessions: Array<ReturnType<typeof useSessionStore.getState>['sessions'][number] & { host?: string; hostName?: string; offline?: boolean }>;
  shownAgentId: string | null;
  selectCreatedSession: (id: string, focusOwner?: Element | null) => boolean;
  showError: ReturnType<typeof useToast>['showError'];
}

function besideWhenStackIsFull(
  desktop: Desktop,
  direction: TerminalSplitDirection,
  anchorPaneId: string | undefined,
  report: (message: string) => void,
): TerminalSplitDirection {
  const tileHeightPx = stackRoomPx(desktop.id);
  const tree = parseLayoutJSON(desktop.tree_json);
  const anchor = anchorPaneId ?? desktop.active_pane_id;
  if (direction !== 'horizontal' || tileHeightPx === null || !tree || !anchor) return direction;
  const message = stackLimitMessage(tree, anchor, tileHeightPx);
  if (!message) return direction;
  report(message);
  return 'vertical';
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
  const hasAvailableAgents = hasAnySelectableAgents(agentAvailability);
  const [sessionCreationJob, setSessionCreationJob] = useState<SessionCreationJob | null>(null);
  const sessionCreationJobIdRef = useRef(0);
  const worktreeSessionCreatingRef = useRef(false);

  const [locationPickerInitialAgent, setLocationPickerInitialAgent] = useState<
    SessionAgent | undefined
  >();
  const [locationPickerOpen, setLocationPickerOpen] = useState(false);
  const [locationPickerOrigin, setLocationPickerOrigin] = useState<ShortcutSource | null>(null);
  const locationPickerSessionDirection = useRef<TerminalSplitDirection>('vertical');

  const nextSplitSessionLabel = useCallback(
    (agent: SessionAgent) => {
      const normalizedAgent = normalizeSessionAgent(agent, 'codex');
      if (normalizedAgent === 'shell') return '';
      const base = normalizedAgent;
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
    (direction: TerminalSplitDirection = 'vertical', agent?: SessionAgent, source?: ShortcutSource) => {
      if (!source && inlineLauncherFocusRef.current && !agent) {
        inlineLauncherFocusRef.current();
        return;
      }
      setLocationPickerInitialAgent(agent);
      setLocationPickerOrigin(source ?? null);
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
      direction: TerminalSplitDirection;
      anchorPaneId?: string;
      spawnedFrom?: string;
      desktopId?: string;
      host?: string;
    }) => {
      // A pick lands on the desktop it was made for, even when it answers after the user moved on;
      // then it starts in the background instead of pulling the user back.
      const current = currentDesktopForLaunch();
      const desktop =
        useProfilesStore.getState().desktops.find((entry) => entry.id === spawn.desktopId) ??
        current;
      const target = launchTarget(desktop, besideWhenStackIsFull(desktop, spawn.direction, spawn.anchorPaneId, showError), spawn.anchorPaneId);
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
        );
        const spawnArgs = takeSessionSpawnArgs(spawn.sessionId, 80, 24);
        if (!spawnArgs) {
          throw new Error('Session spawn arguments were not prepared.');
        }
        await ptySpawn({
          args: {
            ...spawnArgs,
            ...(spawn.host ? { host: spawn.host } : {}),
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
    ) =>
      spawnOnCurrentDesktop({
        sessionId: providedSessionId || crypto.randomUUID(),
        label,
        cwd,
        agent,
        yoloMode,
        direction: 'vertical',
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
      const targetDesktop = options.desktopId
        ? useProfilesStore.getState().desktops.find((desktop) => desktop.id === options.desktopId) ?? currentDesktop
        : currentDesktop;
      const focusedSessionId = launchTarget(
        targetDesktop,
        direction,
        targetPaneId,
      ).focusedSessionId;
      const baseId = options.baseSessionId ?? (targetDesktop.id === currentDesktop.id ? activeLocalSession?.id : undefined) ?? focusedSessionId;
      const base = sessions.find((session) => session.id === baseId) ?? null;
      if (base?.offline) {
        showError(`${base.hostName} is unreachable; this needs it now`);
        return;
      }
      const cwd = options.cwd || base?.cwd;
      if (!cwd) {
        const tree = parseLayoutJSON(targetDesktop.tree_json);
        if (isTauri() && targetPaneId && poppedLeaves(tree).some((leaf) => leafSlotId(leaf) === targetPaneId)) {
          try {
            await invoke('tile_window_raise_main');
          } catch (error) {
            showError(`Could not show the location picker: ${String(error)}`);
            return;
          }
        }
        handleNewSession(direction, undefined, targetPaneId ? { desktopId: targetDesktop.id, leafId: targetPaneId } : undefined);
        return;
      }
      try {
        await spawnOnCurrentDesktop({
          sessionId: crypto.randomUUID(),
          label: options.label || nextSplitSessionLabel(agent),
          cwd,
          agent,
          yoloMode: options.yoloMode ?? base?.yoloMode,
          direction,
          anchorPaneId: targetPaneId,
          spawnedFrom: base?.id,
          host: base?.host,
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
      desktopId?: string;
      host?: string;
      origin?: ShortcutSource;
    }): Promise<string | null> => {
      const current = currentDesktopForLaunch();
      const desktopId = pick.desktopId ?? pick.origin?.desktopId;
      const desktop = useProfilesStore.getState().desktops.find((entry) => entry.id === desktopId) ?? current;
      const origin = pick.origin?.desktopId === desktop.id ? pick.origin : undefined;
      const direction = locationPickerSessionDirection.current;
      const baseId = origin
        ? desktop.panes.find((pane) => pane.pane_id === origin.leafId)?.session_id
        : (desktop.id === current.id ? activeLocalSession?.id : undefined) ?? launchTarget(desktop, direction).focusedSessionId;
      return await spawnOnCurrentDesktop({
        sessionId: crypto.randomUUID(),
        label: pick.label,
        cwd: pick.cwd,
        agent: pick.agent,
        yoloMode: pick.yoloMode,
        direction,
        spawnedFrom: baseId || undefined,
        desktopId,
        anchorPaneId: origin?.leafId,
        host: pick.host,
      });
    },
    [activeLocalSession, spawnOnCurrentDesktop],
  );

  const startLocation = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      yoloMode = false,
      { desktopId, host, origin }: { desktopId?: string; host?: string; origin?: ShortcutSource } = {},
    ) => {
      if (!host && agent !== TERMINAL_AGENT && !hasAvailableAgents)
        throw new Error('No supported agent CLI found in PATH.');
      const selectedAgent =
        host || agent === TERMINAL_AGENT
          ? agent
          : resolvePreferredAgent(agent, agentAvailability, 'codex');
      return launchPicked({
        label: selectedAgent === TERMINAL_AGENT ? '' : path.split('/').pop() || 'session',
        cwd: path,
        agent: selectedAgent,
        yoloMode,
        desktopId,
        host,
        origin,
      });
    },
    [agentAvailability, hasAvailableAgents, launchPicked],
  );

  const launchLocation = useCallback(
    async (
      path: string,
      agent: SessionAgent,
      yoloMode = false,
      desktopId?: string,
      origin?: ShortcutSource,
    ) => {
      if (agent !== TERMINAL_AGENT && !hasAvailableAgents) {
        showError('No supported agent CLI found in PATH.');
        return;
      }
      const selectedAgent =
        agent === TERMINAL_AGENT
          ? TERMINAL_AGENT
          : resolvePreferredAgent(agent, agentAvailability, 'codex');
      try {
        await launchPicked({
          label: selectedAgent === TERMINAL_AGENT ? '' : path.split('/').pop() || 'session',
          cwd: path,
          agent: selectedAgent,
          yoloMode,
          desktopId,
          origin,
        });
      } catch (error) {
        showError(error instanceof Error ? error.message : 'Failed to start the agent');
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
      {
        desktopId,
        host,
        origin,
        onCreated,
      }: { desktopId?: string; host?: string; origin?: ShortcutSource; onCreated?: (path: string) => void } = {},
    ) => {
      const pickedDesktopId = desktopId ?? origin?.desktopId ?? currentDesktopForLaunch().id;
      const result = await sendCreateWorktree(mainRepo, branch, undefined, startingFrom, host);
      if (!result.success || !result.path)
        throw new Error(result.error || 'Failed to create worktree');
      onCreated?.(result.path);
      return startLocation(result.path, agent, yoloMode, {
        desktopId: pickedDesktopId,
        host,
        origin,
      });
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
      pickedOnDesktopId?: string,
      origin?: ShortcutSource,
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
          {
            desktopId: pickedOnDesktopId,
            origin,
            onCreated: (path) => {
              setSessionCreationJob((current) =>
                current?.id === jobId ? { ...current, path, phase: 'starting_session' } : current,
              );
            },
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
    setLocationPickerOrigin(null);
  }, []);

  return {
    sessionCreationJob,
    setSessionCreationJob,
    launchAgent,
    createSessionForUiAutomation,
    locationPickerOpen,
    locationPickerInitialAgent,
    locationPickerOrigin,
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
