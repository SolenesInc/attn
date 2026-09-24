import type { DaemonSessionSnapshot, Session } from '../store/sessions';
import type { DaemonWorkspace } from '../hooks/useDaemonSocket';
import {
  createAgentHistory,
  moveAgentHistory,
  recordAgentVisit,
  type AgentHistoryDirection,
  type AgentHistoryState,
} from './agentHistory';
import {
  advanceAfterTurnClosed,
  buildQueueBands,
  headOfQueue,
  isCrewQueueEnabled,
  isQueueModeEnabled,
  type QueueBands,
  type QueueBandSession,
} from '../utils/queueBands';
import {
  buildWorkspaceViewModels,
  filterSessionsRepresentedInWorkspaceLayouts,
} from '../utils/workspaceViewModels';
import { selectWorkspacePane, type WorkspacePaneSelections } from './workspacePaneSelection';

export type AppView = 'dashboard' | 'session' | 'grid';
export type StateUpdate<T> = T | ((previous: T) => T);
export interface TileSelection {
  workspaceId: string;
  tileId: string;
}
export interface SessionNavigationState {
  activeSessionId: string | null;
  recentSessionIds: string[];
  agentHistory: AgentHistoryState;
  view: AppView;
  followNextTurn: boolean;
  selectedSessionlessWorkspaceId: string | null;
  selectedTile: TileSelection | null;
  pendingSelection: { sessionId: string; seen: boolean } | null;
  focusRequest: { sessionId: string; paneId: string } | null;
  utilityFocusRequestToken: number;
  workspacePaneSelections: WorkspacePaneSelections;
}

export function initialSessionNavigation(): SessionNavigationState {
  return {
    activeSessionId: null,
    recentSessionIds: [],
    agentHistory: createAgentHistory(),
    view: 'dashboard',
    followNextTurn: false,
    selectedSessionlessWorkspaceId: null,
    selectedTile: null,
    pendingSelection: null,
    focusRequest: null,
    utilityFocusRequestToken: 0,
    workspacePaneSelections: {},
  };
}

export function activateSession(
  state: SessionNavigationState,
  id: string | null,
): SessionNavigationState {
  const recent =
    state.activeSessionId && state.activeSessionId !== id
      ? [
          state.activeSessionId,
          ...state.recentSessionIds.filter((entry) => entry !== state.activeSessionId),
        ]
      : state.recentSessionIds;
  return {
    ...state,
    activeSessionId: id,
    pendingSelection: null,
    focusRequest: null,
    view: id ? 'session' : state.view,
    followNextTurn: id ? false : state.followNextTurn,
    selectedTile: id ? null : state.selectedTile,
    selectedSessionlessWorkspaceId: id ? null : state.selectedSessionlessWorkspaceId,
    recentSessionIds: recent.filter((entry) => entry !== id),
    agentHistory:
      id && id !== state.activeSessionId
        ? recordAgentVisit(state.agentHistory, id)
        : state.agentHistory,
  };
}

export function selectAgent(
  state: SessionNavigationState,
  sessions: Session[],
  sessionId: string,
  paneId?: string,
): SessionNavigationState {
  const session = sessions.find((entry) => entry.id === sessionId);
  const pane = session?.workspace.agents.find(
    (entry) => entry.sessionId === sessionId && (!paneId || entry.id === paneId),
  );
  if (!session || !pane)
    return {
      ...state,
      focusRequest: null,
      pendingSelection: paneId ? null : { sessionId, seen: Boolean(session) },
      followNextTurn: false,
    };
  return {
    ...activateSession(state, sessionId),
    workspacePaneSelections: selectWorkspacePane(state.workspacePaneSelections, session, pane.id),
    focusRequest: { sessionId, paneId: pane.id },
    utilityFocusRequestToken: state.utilityFocusRequestToken + 1,
  };
}

export function enterHome(
  state: SessionNavigationState,
  followNextTurn: boolean,
): SessionNavigationState {
  return {
    ...activateSession(state, null),
    view: 'dashboard',
    followNextTurn,
    selectedSessionlessWorkspaceId: null,
    selectedTile: null,
  };
}

export function changeView(
  state: SessionNavigationState,
  update: StateUpdate<AppView>,
): SessionNavigationState {
  const view = typeof update === 'function' ? update(state.view) : update;
  return {
    ...state,
    view,
    pendingSelection: null,
    focusRequest: null,
    followNextTurn: view === 'dashboard' && state.followNextTurn,
  };
}

export function navigateHistory(
  state: SessionNavigationState,
  sessions: Session[],
  direction: AgentHistoryDirection,
  resumeCurrent: boolean,
): SessionNavigationState {
  const move = moveAgentHistory(
    state.agentHistory,
    direction,
    new Set(sessions.map((session) => session.id)),
    resumeCurrent,
  );
  const next = {
    ...state,
    agentHistory: move.state,
    pendingSelection: null,
    focusRequest: null,
    followNextTurn: false,
  };
  return move.targetSessionId
    ? {
        ...selectAgent(activateSession(next, move.targetSessionId), sessions, move.targetSessionId),
        agentHistory: move.state,
      }
    : next;
}

export function reconcilePendingSelection(
  state: SessionNavigationState,
  sessions: Session[],
): SessionNavigationState {
  const pending = state.pendingSelection;
  if (!pending) return state;
  const exists = sessions.some((session) => session.id === pending.sessionId);
  if (!exists) return pending.seen ? { ...state, pendingSelection: null } : state;
  return selectAgent(state, sessions, pending.sessionId);
}

export function sessionAttentionFields(session: DaemonSessionSnapshot | undefined) {
  return {
    chiefOfStaff: session?.chief_of_staff ?? false,
    turnOwed: session?.turn_owed ?? false,
    turnOpenedAt: session?.turn_opened_at,
    turnSnoozedUntil: session?.turn_snoozed_until,
    crewMember: session?.crew_member,
    parentSessionId: session?.parent_session_id,
  };
}

export function navigationQueue(
  sessions: DaemonSessionSnapshot[],
  workspaces: DaemonWorkspace[],
  settings: Record<string, string>,
): QueueBands<QueueBandSession> | null {
  if (!isQueueModeEnabled(settings)) return null;
  const queueSessions = sessions.map((session) => ({
    ...session,
    ...sessionAttentionFields(session),
  }));
  const visible = filterSessionsRepresentedInWorkspaceLayouts(workspaces, queueSessions);
  const views = buildWorkspaceViewModels(workspaces, visible).filter(
    (workspace) => !workspace.muted,
  );
  return buildQueueBands(views, { crewInQueue: isCrewQueueEnabled(settings) });
}

export function advanceQueue(
  state: SessionNavigationState,
  sessions: Session[],
  previous: QueueBands<QueueBandSession> | null,
  next: QueueBands<QueueBandSession> | null,
): SessionNavigationState {
  if (!next || state.pendingSelection) return state;
  if (state.view === 'dashboard' && state.followNextTurn) {
    const target = headOfQueue(next);
    return target ? selectAgent(state, sessions, target.session.id) : state;
  }
  if (state.view !== 'session' || state.selectedSessionlessWorkspaceId || state.selectedTile)
    return state;
  const advance = advanceAfterTurnClosed(previous?.turns ?? [], next, state.activeSessionId);
  if (!advance) return state;
  return advance.to === 'session'
    ? selectAgent(state, sessions, advance.row.session.id)
    : enterHome(state, true);
}
