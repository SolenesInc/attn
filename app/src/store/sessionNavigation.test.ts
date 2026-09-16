import { beforeEach, describe, expect, it } from 'vitest';
import { useSessionStore, type DaemonSessionSnapshot } from './sessions';
import type { DaemonWorkspace } from '../hooks/useDaemonSocket';
import {
  WorkspaceLayoutPaneKind,
  WorkspaceLayoutPaneStatus,
  WorkspaceStatus,
} from '../types/generated';

const state = () => useSessionStore.getState();
const snapshot = (id: string, owed = false): DaemonSessionSnapshot => ({
  id,
  label: id,
  agent: 'claude',
  state: 'working',
  directory: `/tmp/${id}`,
  workspace_id: `workspace-${id}`,
  turn_owed: owed,
  turn_opened_at: id,
});
const workspace = (id: string): DaemonWorkspace => ({
  id: `workspace-${id}`,
  title: id,
  directory: `/tmp/${id}`,
  status: WorkspaceStatus.Working,
  muted: false,
  pinned: false,
  rank: id,
  layout: {
    workspace_id: `workspace-${id}`,
    active_pane_id: `pane-${id}`,
    layout_json: JSON.stringify({ type: 'pane', pane_id: `pane-${id}` }),
    panes: [
      {
        workspace_id: `workspace-${id}`,
        pane_id: `pane-${id}`,
        kind: WorkspaceLayoutPaneKind.Agent,
        runtime_id: id,
        session_id: id,
        title: id,
        status: WorkspaceLayoutPaneStatus.Ready,
      },
    ],
  },
});
function load(ids = ['a', 'b']) {
  state().syncFromDaemonWorkspaces(ids.map(workspace));
  state().syncFromDaemonSessions(ids.map((id) => snapshot(id, true)));
  state().syncNavigationSettings({ queue_mode_enabled: 'true' });
}

beforeEach(() => useSessionStore.setState(useSessionStore.getInitialState(), true));

describe('session navigation at daemon ingestion', () => {
  it('advances a closed turn with the view, pane and history already consistent in the only notification', () => {
    load();
    state().selectAgent('a');
    const observed: string[] = [];
    const unsubscribe = useSessionStore.subscribe((s) =>
      observed.push(
        `${s.activeSessionId}:${s.view}:${s.workspacePaneSelections['workspace-b'].activePaneId}:${s.agentHistory.entries.join(',')}`,
      ),
    );
    state().syncFromDaemonSessions([snapshot('a'), snapshot('b', true)]);
    unsubscribe();
    expect(observed).toEqual(['b:session:pane-b:a,b']);
  });

  it('waits at Home after the queue empties and selects the oldest new turn', () => {
    load();
    state().selectAgent('b');
    state().syncFromDaemonSessions([snapshot('a'), snapshot('b')]);
    expect(state()).toMatchObject({
      activeSessionId: null,
      view: 'dashboard',
      followNextTurn: true,
    });
    state().syncFromDaemonSessions([snapshot('b', true), snapshot('a', true)]);
    expect(state()).toMatchObject({
      activeSessionId: 'a',
      view: 'session',
      followNextTurn: false,
    });
  });

  it('does not derive selection from the head when the user chooses a settled agent', () => {
    load();
    state().syncFromDaemonSessions([snapshot('a'), snapshot('b', true)]);
    state().selectAgent('a');
    state().syncFromDaemonSessions([snapshot('a'), snapshot('b', true)]);
    expect(state().activeSessionId).toBe('a');
  });

  it.each(['home', 'grid', 'workspace', 'tile', 'history'] as const)(
    '%s navigation cancels waiting for the next turn',
    (action) => {
      load();
      state().selectAgent('a');
      state().syncFromDaemonSessions([snapshot('a'), snapshot('b')]);
      if (action === 'home') state().goToDashboard();
      if (action === 'grid') {
        state().setView('grid');
        state().setView('dashboard');
      }
      if (action === 'workspace') state().selectSessionlessWorkspace('notes');
      if (action === 'tile') state().setSelectedTile({ workspaceId: 'notes', tileId: 'note' });
      if (action === 'history') state().navigateAgentHistory('back', true);
      const selected = state().activeSessionId;
      state().syncFromDaemonSessions([snapshot('a'), snapshot('b', true)]);
      expect(state().activeSessionId).toBe(selected);
      expect(state().followNextTurn).toBe(false);
    },
  );

  it('explicitly arming follow selects a turn that is already available', () => {
    load();
    state().setFollowNextTurn(true);
    expect(state().activeSessionId).toBe('a');
  });

  it.each([
    'home',
    'grid',
    'workspace',
    'tile',
    'session',
    'pane',
    'back',
    'forward',
    'cancel',
  ] as const)('%s cancels a deferred selection before its pane arrives', (action) => {
    load();
    state().selectAgent('a');
    state().selectAgent('b');
    expect(state().selectAgent('c')).toBe(false);
    if (action === 'home') state().goToDashboard();
    if (action === 'grid') state().setView('grid');
    if (action === 'workspace') state().selectSessionlessWorkspace('notes');
    if (action === 'tile') state().setSelectedTile({ workspaceId: 'notes', tileId: 'note' });
    if (action === 'session') state().selectAgent('a');
    if (action === 'pane') state().selectAgentPane('a', 'pane-a');
    if (action === 'back' || action === 'forward') state().navigateAgentHistory(action);
    if (action === 'cancel') state().cancelPendingSelection();
    state().syncFromDaemonSessions(['a', 'b', 'c'].map((id) => snapshot(id, true)));
    state().syncFromDaemonWorkspaces(['a', 'b', 'c'].map(workspace));
    expect(state().activeSessionId).not.toBe('c');
    expect(state().pendingSelection).toBeNull();
  });

  it.each(['session-first', 'pane-first'] as const)(
    'completes a deferred selection in the data update that makes it ready (%s)',
    (order) => {
      load();
      state().selectAgent('a');
      state().selectAgent('c');
      const sessions = () =>
        state().syncFromDaemonSessions(['a', 'b', 'c'].map((id) => snapshot(id, true)));
      const panes = () => state().syncFromDaemonWorkspaces(['a', 'b', 'c'].map(workspace));
      if (order === 'session-first') {
        sessions();
        expect(state().activeSessionId).toBe('a');
        panes();
      } else {
        panes();
        sessions();
      }
      expect(state()).toMatchObject({
        activeSessionId: 'c',
        view: 'session',
        pendingSelection: null,
        focusRequest: { sessionId: 'c', paneId: 'pane-c' },
      });
      expect(state().agentHistory.entries).toEqual(['a', 'c']);
    },
  );

  it('only honors the latest pending target when both arrive', () => {
    load();
    state().selectAgent('c');
    state().selectAgent('d');
    state().syncFromDaemonSessions(['a', 'b', 'c', 'd'].map((id) => snapshot(id, true)));
    state().syncFromDaemonWorkspaces(['a', 'b', 'c', 'd'].map(workspace));
    expect(state().activeSessionId).toBe('d');
  });

  it('reads current data from a callback captured before creation', () => {
    load();
    const select = state().selectAgent;
    state().syncFromDaemonSessions(['a', 'b', 'c'].map((id) => snapshot(id, true)));
    state().syncFromDaemonWorkspaces(['a', 'b', 'c'].map(workspace));
    expect(select('c')).toBe(true);
    expect(state().pendingSelection).toBeNull();
  });

  it('cancels a pending target once an observed session disappears', () => {
    load();
    state().selectAgent('a');
    state().syncFromDaemonSessions(['a', 'b', 'c'].map((id) => snapshot(id, true)));
    state().selectAgent('c');
    state().syncFromDaemonSessions(['a', 'b'].map((id) => snapshot(id, true)));
    state().syncFromDaemonWorkspaces(['a', 'b', 'c'].map(workspace));
    state().syncFromDaemonSessions(['a', 'b', 'c'].map((id) => snapshot(id, true)));
    expect(state().activeSessionId).toBe('a');
    expect(state().pendingSelection).toBeNull();
  });

  it('keeps a tile-only workspace selected when a background session closes', () => {
    load();
    state().selectAgent('a');
    state().selectSessionlessWorkspace('notes');
    state().syncFromDaemonSessions([snapshot('b', true)]);
    expect(state()).toMatchObject({ selectedSessionlessWorkspaceId: 'notes', view: 'session' });
  });

  it('finishes an explicit pending selection without letting queue advancement override it', () => {
    load();
    state().selectAgent('a');
    state().syncFromDaemonSessions(['a', 'b', 'c'].map((id) => snapshot(id, true)));
    state().selectAgent('c');
    state().syncFromDaemonSessions([snapshot('a', true), snapshot('b', true), snapshot('c')]);
    state().syncFromDaemonWorkspaces(['a', 'b', 'c'].map(workspace));
    expect(state()).toMatchObject({ activeSessionId: 'c', pendingSelection: null });
  });

  it('validates explicit panes against the requested session', () => {
    load();
    expect(state().selectAgentPane('a', 'pane-b')).toBe(false);
    expect(state().activeSessionId).toBeNull();
    expect(state().selectAgentPane('a', 'pane-a')).toBe(true);
    expect(state().activeSessionId).toBe('a');
  });
});
