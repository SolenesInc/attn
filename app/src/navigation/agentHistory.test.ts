import { describe, expect, it } from 'vitest';
import {
  AGENT_HISTORY_LIMIT,
  createAgentHistory,
  moveAgentHistory,
  recordAgentVisit,
  reconcileAgentHistory,
  type AgentHistoryState,
} from './agentHistory';

function visit(ids: string[]): AgentHistoryState {
  return ids.reduce(recordAgentVisit, createAgentHistory());
}

describe('agent history', () => {
  it('starts empty', () => {
    expect(createAgentHistory()).toEqual({ entries: [], cursor: -1 });
  });

  it('records visits, ignoring empty and consecutive ids', () => {
    const initial = visit(['a', 'b']);
    const unchanged = recordAgentVisit(initial, 'b');

    expect(recordAgentVisit(initial, '')).toBe(initial);
    expect(unchanged).toBe(initial);
    expect(initial).toEqual({ entries: ['a', 'b'], cursor: 1 });
    expect(recordAgentVisit(initial, 'a')).toEqual({ entries: ['a', 'b', 'a'], cursor: 2 });
  });

  it('truncates forward visits before recording a new branch', () => {
    const state = moveAgentHistory(
      visit(['a', 'b', 'c']),
      'back',
      new Set(['a', 'b', 'c', 'd']),
      false,
    ).state;

    expect(state).toEqual({ entries: ['a', 'b', 'c'], cursor: 1 });
    expect(recordAgentVisit(state, 'd')).toEqual({ entries: ['a', 'b', 'd'], cursor: 2 });
  });

  it('keeps only the newest entries at the limit and shifts the cursor', () => {
    const state = visit(Array.from({ length: AGENT_HISTORY_LIMIT + 1 }, (_, index) => `s-${index}`));

    expect(state.entries).toHaveLength(AGENT_HISTORY_LIMIT);
    expect(state.entries[0]).toBe('s-1');
    expect(state.entries[state.entries.length - 1]).toBe(`s-${AGENT_HISTORY_LIMIT}`);
    expect(state.cursor).toBe(AGENT_HISTORY_LIMIT - 1);
  });

  it('reconciles vanished sessions while retaining valid repeats and cursor position', () => {
    const state = visit(['a', 'b', 'a', 'c', 'd']);
    const moved = moveAgentHistory(state, 'back', new Set(['a', 'b', 'c', 'd']), false).state;
    const reconciled = reconcileAgentHistory(moved, new Set(['a', 'c', 'd']));

    expect(moved).toEqual({ entries: ['a', 'b', 'a', 'c', 'd'], cursor: 3 });
    expect(reconciled).toEqual({ entries: ['a', 'a', 'c', 'd'], cursor: 2 });
  });

  it('returns empty history when no entries survive reconciliation', () => {
    expect(reconcileAgentHistory(visit(['a', 'b']), new Set())).toEqual({ entries: [], cursor: -1 });
  });

  it('moves backward and forward without adding visits', () => {
    const live = new Set(['a', 'b', 'c']);
    const initial = visit(['a', 'b', 'c']);
    const back = moveAgentHistory(initial, 'back', live, false);
    const forward = moveAgentHistory(back.state, 'forward', live, false);

    expect(back).toEqual({ state: { entries: ['a', 'b', 'c'], cursor: 1 }, targetSessionId: 'b' });
    expect(forward).toEqual({ state: { entries: ['a', 'b', 'c'], cursor: 2 }, targetSessionId: 'c' });
    expect(moveAgentHistory(initial, 'forward', live, false)).toEqual({
      state: initial,
      targetSessionId: null,
    });
    expect(moveAgentHistory(visit(['a']), 'back', live, false)).toEqual({
      state: { entries: ['a'], cursor: 0 },
      targetSessionId: null,
    });
  });

  it('resumes the current entry from a non-session view only when moving back', () => {
    const live = new Set(['a', 'b']);
    const state = visit(['a', 'b']);

    expect(moveAgentHistory(state, 'back', live, true)).toEqual({
      state,
      targetSessionId: 'b',
    });
    expect(moveAgentHistory(state, 'forward', live, true)).toEqual({
      state,
      targetSessionId: null,
    });
  });

  it('reconciles before traversing so stale targets are skipped', () => {
    const move = moveAgentHistory(
      { entries: ['a', 'stale', 'b'], cursor: 2 },
      'back',
      new Set(['a', 'b']),
      false,
    );

    expect(move).toEqual({
      state: { entries: ['a', 'b'], cursor: 0 },
      targetSessionId: 'a',
    });
  });
});
