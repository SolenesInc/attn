import { create } from 'zustand';
import { initialSessionNavigation, type SessionNavigationState } from '../navigation/sessionNavigation';
import { createSessionNavigationActions, reconcileSessionNavigation, type SessionNavigationActions } from './sessionNavigationSlice';
import type { QueueBands, QueueBandSession } from '../utils/queueBands';
import type { UISessionState } from '../types/sessionState';
import { normalizeSessionState } from '../types/sessionState';
import type { SessionAgent } from '../types/sessionAgent';
import { normalizeSessionAgent } from '../types/sessionAgent';
import type { AutomationProvenance, Desktop, SessionPullRequest } from '../types/generated';
import { listenPtyEvents, ptyReload, type PtySpawnArgs } from '../pty/bridge';
import {
  createDefaultWorkspaceState,
  type TerminalWorkspaceSnapshot,
  type TerminalWorkspaceState,
} from '../types/workspace';
import { desktopSnapshot } from '../utils/desktops';
import {
  recordAgentVisit,
  reconcileAgentHistory,
} from '../navigation/agentHistory';

export type { TerminalWorkspaceState };

// A reload's exit event looks like a clean voluntary quit (code 0, no signal),
// which would trip auto-close-on-clean-exit and tear the pane down mid-restore.
const reloadingSessionIds = new Set<string>();

export function isSessionReloading(id: string): boolean {
  return reloadingSessionIds.has(id);
}

export interface Session {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  workspaceId: string;
  profileId: string;
  desktopId: string;
  agent: SessionAgent;
  endpointId?: string;
  yoloMode?: boolean;
  chiefOfStaff?: boolean;
  autoMode?: boolean;
  transcriptMatched: boolean;
  creating?: boolean;
  branch?: string;
  isWorktree?: boolean;
  automation?: AutomationProvenance;
  pullRequests?: SessionPullRequest[];
  desktop: TerminalWorkspaceState;
  daemonActivePaneId: string;
}

export interface DaemonSessionSnapshot {
  chief_of_staff?: boolean;
  turn_owed?: boolean;
  turn_opened_at?: string;
  turn_snoozed_until?: string;
  crew_member?: string;
  parent_session_id?: string;
  id: string;
  label: string;
  agent?: string;
  directory: string;
  workspace_id: string;
  profile_id?: string;
  endpoint_id?: string;
  state: string;
  branch?: string;
  is_worktree?: boolean;
  automation?: AutomationProvenance;
  pull_requests?: SessionPullRequest[];
}

interface LauncherConfig {
  executables: Record<string, string>;
}

export interface SessionStore extends SessionNavigationState, SessionNavigationActions {
  sessions: Session[];
  navigationSessions: DaemonSessionSnapshot[];
  navigationProfileId: string;
  navigationDesktops: Desktop[];
  navigationSettings: Record<string, string>;
  navigationQueue: QueueBands<QueueBandSession> | null;
  connected: boolean;
  launcherConfig: LauncherConfig;
  desktopSnapshots: Record<string, TerminalWorkspaceSnapshot>;
  desktopIdBySessionId: Record<string, string>;

  connect: () => Promise<void>;
  createSession: (
    label: string,
    cwd: string,
    id: string | undefined,
    agent: SessionAgent | undefined,
    endpointId: string | undefined,
    yoloMode: boolean | undefined,
    workspaceId: string | undefined,
    chiefOfStaff?: boolean,
    autoMode?: boolean,
  ) => Promise<string>;
  closeSession: (id: string) => void;
  removeSessionLocalState: (id: string) => void;
  takeSessionSpawnArgs: (id: string, cols: number, rows: number) => PtySpawnArgs | null;
  reloadSession: (id: string, size?: { cols: number; rows: number }) => Promise<void>;
  setLauncherConfig: (config: LauncherConfig) => void;
  syncFromDaemonSessions: (daemonSessions: DaemonSessionSnapshot[]) => void;
  syncFromArrangement: (profileId: string, desktops: Desktop[]) => void;
}

const MIN_STABLE_COLS = 20;
const MIN_STABLE_ROWS = 8;

interface TestSession {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  agent?: SessionAgent;
  workspaceId: string;
  branch?: string;
  isWorktree?: boolean;
}

declare global {
  interface Window {
    __TEST_INJECT_SESSION?: (session: TestSession) => void;
    __TEST_UPDATE_SESSION_STATE?: (id: string, state: UISessionState) => void;
    __TEST_GET_SESSIONS?: () => Array<{ id: string; label: string; cwd: string }>;
    __TEST_SET_SESSION_WORKSPACE?: (sessionId: string, workspace: TerminalWorkspaceState, daemonActivePaneId?: string) => void;
  }
}

function samePullRequests(
  a: readonly SessionPullRequest[] | undefined,
  b: readonly SessionPullRequest[] | undefined,
): boolean {
  if (a === b) return true;
  if (!a || !b || a.length !== b.length) return (a?.length ?? 0) === 0 && (b?.length ?? 0) === 0;
  return a.every((pr, index) => {
    const other = b[index];
    return pr.url === other.url
      && pr.state === other.state
      && pr.title === other.title
      && pr.ci_status === other.ci_status
      && pr.review_status === other.review_status
      && pr.mergeable_state === other.mergeable_state
      && pr.status_fetched_at === other.status_fetched_at;
  });
}


export const useSessionStore = create<SessionStore>((set, get) => ({
  sessions: [],
  ...initialSessionNavigation(),
  ...createSessionNavigationActions(set, get),
  navigationSessions: [],
  navigationProfileId: '',
  navigationDesktops: [],
  navigationSettings: {},
  navigationQueue: null,
  connected: false,
  launcherConfig: {
    executables: {},
  },
  desktopSnapshots: {},
  desktopIdBySessionId: {},

  connect: async () => {
    if (get().connected) return;

    try {
      await listenPtyEvents((event) => {
        const msg = event.payload;
        const { sessions } = get();

        if (msg.event === 'transcript') {
          if (typeof msg.matched === 'boolean') {
            const updated = sessions.map((entry) =>
              entry.id === msg.id ? { ...entry, transcriptMatched: msg.matched } : entry,
            );
            set({ sessions: updated });
          }
          return;
        }
      });

      set({ connected: true });
    } catch (e) {
      console.error('[Session] Connect failed:', e);
    }
  },

  createSession: async (
    label: string,
    cwd: string,
    providedId: string | undefined,
    agent: SessionAgent | undefined,
    endpointId: string | undefined,
    yoloMode: boolean | undefined,
    providedWorkspaceId: string | undefined,
    chiefOfStaff?: boolean,
    autoMode?: boolean,
  ) => {
    const id = providedId || crypto.randomUUID();
    const workspaceId = providedWorkspaceId ?? '';
    const resolvedAgent: SessionAgent = agent ?? 'claude';
    const session: Session = {
      id,
      label,
      state: 'launching',
      cwd,
      workspaceId,
      profileId: '',
      desktopId: '',
      agent: resolvedAgent,
      endpointId,
      yoloMode: yoloMode ?? false,
      chiefOfStaff: chiefOfStaff ?? false,
      autoMode,
      transcriptMatched: resolvedAgent !== 'codex',
      creating: true,
      desktop: createDefaultWorkspaceState(),
      daemonActivePaneId: '',
    };

    set((state) => ({
      view: 'session', followNextTurn: false, pendingSelection: null, focusRequest: null,
      selectedTile: null,
      sessions: [...state.sessions, session],
      activeSessionId: id,
      agentHistory: recordAgentVisit(state.agentHistory, id),
    }));

    return id;
  },

  removeSessionLocalState: (id: string) => {
    set((state) => {
      const sessions = state.sessions.filter((session) => session.id !== id);
      const liveSessionIds = new Set(sessions.map((session) => session.id));
      const agentHistory = reconcileAgentHistory(state.agentHistory, liveSessionIds);
      return reconcileSessionNavigation(state, {
        sessions,
        agentHistory,
        activeSessionId: state.activeSessionId === id ? null : state.activeSessionId,
      });
    });
  },

  closeSession: (id: string) => {
    get().removeSessionLocalState(id);
  },

  takeSessionSpawnArgs: (id: string, cols: number, rows: number) => {
    const { sessions, launcherConfig } = get();
    const session = sessions.find((entry) => entry.id === id);
    if (!session) {
      return null;
    }
    let resolvedCols = cols > 0 ? cols : 80;
    let resolvedRows = rows > 0 ? rows : 24;
    if (resolvedCols < MIN_STABLE_COLS || resolvedRows < MIN_STABLE_ROWS) {
      resolvedCols = 80;
      resolvedRows = 24;
    }
    const selectedExecutable = launcherConfig.executables[session.agent] || '';
    return {
      id,
      cwd: session.cwd,
      workspace_id: session.workspaceId,
      ...(session.endpointId ? { endpoint_id: session.endpointId } : {}),
      intent: 'create',
      label: session.label,
      cols: resolvedCols,
      rows: resolvedRows,
      shell: false,
      agent: session.agent,
      resume_session_id: null,
      yolo_mode: session.yoloMode ?? null,
      ...(session.chiefOfStaff ? { chief_of_staff: true } : {}),
      // Explicit false is a real answer here, so the field is sent whenever it was
      // set and is never `&&`-collapsed away.
      ...(session.autoMode !== undefined ? { auto_mode: session.autoMode } : {}),
      ...(selectedExecutable ? { executable: selectedExecutable } : {}),
      ...(session.agent === 'claude' && selectedExecutable
        ? { claude_executable: selectedExecutable }
        : {}),
      ...(session.agent === 'codex' && selectedExecutable
        ? { codex_executable: selectedExecutable }
        : {}),
      ...(session.agent === 'copilot' && selectedExecutable
        ? { copilot_executable: selectedExecutable }
        : {}),
    };
  },

  reloadSession: async (id: string, size?: { cols: number; rows: number }) => {
    const { sessions } = get();
    const session = sessions.find((s) => s.id === id);
    if (!session) return;
    let cols = size?.cols && size.cols > 0 ? size.cols : 80;
    let rows = size?.rows && size.rows > 0 ? size.rows : 24;
    if (cols < MIN_STABLE_COLS || rows < MIN_STABLE_ROWS) {
      cols = 80;
      rows = 24;
    }
    reloadingSessionIds.add(id);
    try {
      await ptyReload({ id, cols, rows });
    } finally {
      reloadingSessionIds.delete(id);
    }
  },

  setLauncherConfig: (config: LauncherConfig) => {
    set({ launcherConfig: config });
  },

  syncFromDaemonSessions: (daemonSessions: DaemonSessionSnapshot[]) => {
    set((state) => {
      const existingByID = new Map(state.sessions.map((session) => [session.id, session]));

      const syncedSessions = daemonSessions.map((daemonSession) => {
        const existing = existingByID.get(daemonSession.id);
        const nextDesktopId = state.desktopIdBySessionId[daemonSession.id] ?? '';
        const desktopSnapshot = state.desktopSnapshots[nextDesktopId];
        const nextProfileId = daemonSession.profile_id ?? '';
        const normalizedState = normalizeSessionState(daemonSession.state);
        const nextAgent: SessionAgent = normalizeSessionAgent(daemonSession.agent, existing?.agent ?? 'codex');
        const nextEndpointId = daemonSession.endpoint_id ?? existing?.endpointId;
        const nextWorkspaceId = daemonSession.workspace_id;
        const nextBranch = daemonSession.branch ?? existing?.branch;
        const nextIsWorktree = daemonSession.is_worktree ?? existing?.isWorktree;

        if (
          existing &&
          !existing.creating &&
          existing.label === daemonSession.label &&
          existing.agent === nextAgent &&
          existing.cwd === daemonSession.directory &&
          existing.workspaceId === nextWorkspaceId &&
          existing.profileId === nextProfileId &&
          existing.desktopId === nextDesktopId &&
          existing.endpointId === nextEndpointId &&
          existing.state === normalizedState &&
          existing.branch === nextBranch &&
          existing.isWorktree === nextIsWorktree &&
          existing.automation?.run_id === daemonSession.automation?.run_id
          && samePullRequests(existing.pullRequests, daemonSession.pull_requests)
        ) {
          return existing;
        }

        return {
          id: daemonSession.id,
          label: daemonSession.label,
          state: normalizedState,
          cwd: daemonSession.directory,
          workspaceId: nextWorkspaceId,
          profileId: nextProfileId,
          desktopId: nextDesktopId,
          agent: nextAgent,
          endpointId: nextEndpointId,
          yoloMode: existing?.yoloMode,
          transcriptMatched: existing?.transcriptMatched ?? nextAgent !== 'codex',
          branch: nextBranch,
          isWorktree: nextIsWorktree,
          automation: daemonSession.automation,
          pullRequests: daemonSession.pull_requests,
          desktop: desktopSnapshot?.workspace ?? createDefaultWorkspaceState(),
          daemonActivePaneId: desktopSnapshot?.daemonActivePaneId ?? '',
        } satisfies Session;
      });

      const pendingSessions = state.sessions.filter((session) => (
        !syncedSessions.some((synced) => synced.id === session.id)
        && (
          session.creating
          || (
            session.state === 'launching'
            && session.desktop.agents.some((pane) => pane.sessionId === session.id && pane.status === 'spawning')
          )
        )
      ));
      const allSessions = [...syncedSessions, ...pendingSessions];
      const syncedIds = new Set(allSessions.map((session) => session.id));
      const activeSessionId =
        state.activeSessionId && syncedIds.has(state.activeSessionId) ? state.activeSessionId : null;
      return reconcileSessionNavigation(state, {
        navigationSessions: daemonSessions,
        sessions: allSessions,
        activeSessionId,
        agentHistory: reconcileAgentHistory(state.agentHistory, syncedIds),
      });
    });
  },

  syncFromArrangement: (profileId: string, desktops: Desktop[]) => {
    const desktopSnapshots = Object.fromEntries(desktops.map((desktop) => [desktop.id, desktopSnapshot(desktop)]));
    const desktopIdBySessionId = Object.fromEntries(
      desktops.flatMap((desktop) => desktop.panes.map((pane) => [pane.session_id, desktop.id] as const)),
    );
    set((state) => {
      const sessions = state.sessions.map((session) => {
        const desktopId = desktopIdBySessionId[session.id] ?? '';
        const snapshot = desktopSnapshots[desktopId];
        return {
          ...session,
          desktopId,
          desktop: snapshot?.workspace ?? createDefaultWorkspaceState(),
          daemonActivePaneId: snapshot?.daemonActivePaneId ?? '',
        };
      });
      return reconcileSessionNavigation(state, {
        sessions,
        desktopSnapshots,
        desktopIdBySessionId,
        navigationProfileId: profileId,
        navigationDesktops: desktops,
      });
    });
  },
}));

declare global {
  interface Window {
    __TEST_GET_SESSION_INPUT_EVENTS?: (sessionId: string) => Array<{ event: 'connect_terminal' | 'send_to_pty'; data?: string; source?: string }>;
  }
}

if (import.meta.env.DEV) {
  window.__TEST_INJECT_SESSION = (session: TestSession) => {
    const workspaceId = session.workspaceId ?? '';
    useSessionStore.setState((state) => ({
      sessions: [
        ...state.sessions,
        {
          ...session,
          workspaceId,
          profileId: '',
          desktopId: '',
          agent: session.agent ?? 'codex',
          transcriptMatched: (session.agent ?? 'codex') !== 'codex',
          desktop: createDefaultWorkspaceState(),
          daemonActivePaneId: '',
        },
      ],
    }));
  };

  window.__TEST_GET_SESSIONS = () =>
    useSessionStore.getState().sessions.map(({ id, label, cwd }) => ({ id, label, cwd }));

  window.__TEST_UPDATE_SESSION_STATE = (id: string, state: UISessionState) => {
    useSessionStore.setState((s) => ({
      sessions: s.sessions.map((session) =>
        session.id === id ? { ...session, state } : session
      ),
    }));
  };

  window.__TEST_SET_SESSION_WORKSPACE = (sessionId: string, workspace: TerminalWorkspaceState, daemonActivePaneId = workspace.agents[0]?.id || '') => {
    useSessionStore.setState((state) => reconcileSessionNavigation(state, {
      sessions: state.sessions.map((session) =>
        session.id === sessionId
          ? { ...session, desktop: workspace, daemonActivePaneId }
          : session
      ),
    }));
  };

  window.__TEST_GET_SESSION_INPUT_EVENTS = (sessionId: string) =>
    ((window as Window & {
      __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: 'connect_terminal' | 'send_to_pty'; data?: string; source?: string }>;
    }).__TEST_SESSION_INPUT_EVENTS || [])
      .filter((entry) => entry.sessionId === sessionId)
      .map(({ event, data, source }) => ({ event, data, source }));
}
