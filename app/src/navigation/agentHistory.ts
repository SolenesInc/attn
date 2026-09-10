export const AGENT_HISTORY_LIMIT = 1024;

export type AgentHistoryDirection = 'back' | 'forward';

export interface AgentHistoryState {
  entries: string[];
  cursor: number;
}

export interface AgentHistoryMove {
  state: AgentHistoryState;
  targetSessionId: string | null;
}

export function createAgentHistory(): AgentHistoryState {
  return { entries: [], cursor: -1 };
}

export function recordAgentVisit(
  state: AgentHistoryState,
  sessionId: string,
): AgentHistoryState {
  if (!sessionId || state.entries[state.cursor] === sessionId) {
    return state;
  }

  const entries = [...state.entries.slice(0, state.cursor + 1), sessionId];
  const discardedCount = Math.max(0, entries.length - AGENT_HISTORY_LIMIT);
  return {
    entries: discardedCount > 0 ? entries.slice(discardedCount) : entries,
    cursor: entries.length - 1 - discardedCount,
  };
}

export function reconcileAgentHistory(
  state: AgentHistoryState,
  liveSessionIds: ReadonlySet<string>,
): AgentHistoryState {
  if (state.entries.length === 0) {
    return state.cursor === -1 ? state : createAgentHistory();
  }

  const entries = state.entries.filter((sessionId) => liveSessionIds.has(sessionId));
  if (entries.length === 0) {
    return createAgentHistory();
  }

  const survivingEntriesBeforeCursor = state.cursor < 0
    ? 0
    : state.entries
      .slice(0, state.cursor)
      .filter((sessionId) => liveSessionIds.has(sessionId))
      .length;
  const cursor = Math.min(survivingEntriesBeforeCursor, entries.length - 1);

  if (
    entries.length === state.entries.length
    && entries.every((sessionId, index) => sessionId === state.entries[index])
    && cursor === state.cursor
  ) {
    return state;
  }

  return { entries, cursor };
}

export function moveAgentHistory(
  state: AgentHistoryState,
  direction: AgentHistoryDirection,
  liveSessionIds: ReadonlySet<string>,
  resumeCurrent: boolean,
): AgentHistoryMove {
  const reconciled = reconcileAgentHistory(state, liveSessionIds);
  if (reconciled.entries.length === 0) {
    return { state: reconciled, targetSessionId: null };
  }

  if (resumeCurrent) {
    return {
      state: reconciled,
      targetSessionId: direction === 'back'
        ? reconciled.entries[reconciled.cursor] ?? null
        : null,
    };
  }

  const nextCursor = reconciled.cursor + (direction === 'back' ? -1 : 1);
  if (nextCursor < 0 || nextCursor >= reconciled.entries.length) {
    return { state: reconciled, targetSessionId: null };
  }

  return {
    state: { entries: [...reconciled.entries], cursor: nextCursor },
    targetSessionId: reconciled.entries[nextCursor] ?? null,
  };
}
