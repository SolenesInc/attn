import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useSessionStore } from './sessions';
import { LayoutPaneKind, LayoutPaneStatus, type Desktop } from '../types/generated';
import type { TerminalLayoutNode } from '../types/desktop';

const { mockPtyReload } = vi.hoisted(() => ({
  mockPtyReload: vi.fn(),
}));

vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('@tauri-apps/api/event', () => ({
  listen: vi.fn().mockResolvedValue(() => {}),
}));

vi.mock('../pty/bridge', async () => {
  const actual = await vi.importActual<typeof import('../pty/bridge')>('../pty/bridge');
  return {
    ...actual,
    ptyReload: mockPtyReload,
  };
});

function desktopWith(
  id: string,
  treeJson: string,
  activePaneId: string,
  panes: Array<{ pane_id: string; session_id: string; title: string; status?: LayoutPaneStatus; error?: string }>,
): Desktop {
  return {
    id,
    profile_id: 'profile',
    name: '',
    order_key: id,
    tree_json: treeJson,
    active_pane_id: activePaneId,
    revision: 1,
    panes: panes.map((pane) => ({
      desktop_id: id,
      kind: LayoutPaneKind.Agent,
      status: LayoutPaneStatus.Ready,
      runtime_id: pane.session_id,
      ...pane,
    })),
  };
}

describe('sessions store', () => {
  beforeEach(() => {
    mockPtyReload.mockReset();
    useSessionStore.setState({
      sessions: [],
      connected: false,
      launcherConfig: { executables: {} },
      desktopSnapshots: {},
      desktopIdBySessionId: {},
    });
  });

  it('creates sessions with a default daemon-owned desktop view model', async () => {
    const sessionId = await useSessionStore.getState().createSession('test', '/tmp/test', 'sess-test', 'codex', undefined, false);
    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);

    expect(session?.desktop).toEqual({
      agents: [],
      layoutTree: null,
    });
    expect(session?.daemonActivePaneId).toBe('');
    expect(useSessionStore.getState().intent).toBeNull();
  });

  it('syncFromDaemonSessions hydrates canonical session data and keeps the session on its desktop', () => {
    const desktop = {
      agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: 'Session', sessionId: 'session-1' }],
      layoutTree: {
        type: 'split' as const,
        splitId: 'root',
        direction: 'vertical' as const,
        ratio: 0.5,
        children: [
          { type: 'pane' as const, paneId: 'pane-session' },
          { type: 'pane' as const, paneId: 'pane-a' },
        ] as [TerminalLayoutNode, TerminalLayoutNode],
      },
    };
    useSessionStore.setState({
      desktopSnapshots: { 'desktop-sess-1': { terminalState: desktop, daemonActivePaneId: 'pane-a' } },
      desktopIdBySessionId: { 'sess-1': 'desktop-sess-1' },
      sessions: [
        {
          id: 'sess-1',
          label: 'Old Label',
          state: 'working',
          cwd: '/tmp/old',
          profileId: '',
          desktopId: 'desktop-sess-1',
          agent: 'codex',
          transcriptMatched: false,
          daemonActivePaneId: 'pane-a',
          desktop: {
            agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' }],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-session' },
                { type: 'pane', paneId: 'pane-a' },
              ],
            },
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([
      {
        id: 'sess-1',
        label: 'New Label',
        agent: 'claude',
        directory: '/tmp/new',
        endpoint_id: 'ep-1',
        state: 'idle',
        branch: 'feature/desktop',
        is_worktree: true,
      },
    ]);

    const session = useSessionStore.getState().sessions[0];
    expect(session).toMatchObject({
      id: 'sess-1',
      label: 'New Label',
      cwd: '/tmp/new',
      state: 'idle',
      agent: 'claude',
      endpointId: 'ep-1',
      branch: 'feature/desktop',
      isWorktree: true,
      daemonActivePaneId: 'pane-a',
    });
    expect(session.desktop).toMatchObject({
      agents: [{ id: 'pane-a', runtimeId: 'runtime-a', title: "Session", sessionId: 'session-1' }],
    });
  });

  it('syncFromDaemonSessions takes the desktop the session is placed on now', () => {
    const sourceLayout = {
      agents: [
        { id: 'pane-source', runtimeId: 'source-session', title: 'Source', sessionId: 'source-session' },
        { id: 'pane-moved', runtimeId: 'moved-session', title: 'Moved', sessionId: 'moved-session' },
      ],
      layoutTree: {
        type: 'split' as const,
        splitId: 'root',
        direction: 'vertical' as const,
        ratio: 0.5,
        children: [
          { type: 'pane' as const, paneId: 'pane-source' },
          { type: 'pane' as const, paneId: 'pane-moved' },
        ] as [TerminalLayoutNode, TerminalLayoutNode],
      },
    };
    const targetLayout = {
      agents: [{ id: 'pane-moved', runtimeId: 'moved-session', title: 'Moved', sessionId: 'moved-session' }],
      layoutTree: { type: 'pane' as const, paneId: 'pane-moved' },
    };
    const movedSession = {
      id: 'moved-session',
      label: 'Moved',
      state: 'working' as const,
      cwd: '/tmp/source',
      profileId: '',
      desktopId: 'desktop-source',
      agent: 'codex' as const,
      transcriptMatched: false,
      daemonActivePaneId: 'pane-moved',
      desktop: sourceLayout,
    };
    useSessionStore.setState({
      sessions: [movedSession],
      desktopSnapshots: {
        'desktop-target': { terminalState: targetLayout, daemonActivePaneId: 'pane-moved' },
      },
      desktopIdBySessionId: { 'moved-session': 'desktop-target' },
    });

    useSessionStore.getState().syncFromDaemonSessions([
      { id: 'moved-session', label: 'Moved', agent: 'codex', directory: '/tmp/target', state: 'working' },
    ]);
    expect(useSessionStore.getState().sessions[0].desktop).toEqual(targetLayout);
    expect(useSessionStore.getState().sessions[0].daemonActivePaneId).toBe('pane-moved');

    useSessionStore.setState({ sessions: [movedSession], desktopSnapshots: {}, desktopIdBySessionId: {} });
    useSessionStore.getState().syncFromDaemonSessions([
      { id: 'moved-session', label: 'Moved', agent: 'codex', directory: '/tmp/target', state: 'working' },
    ]);
    expect(useSessionStore.getState().sessions[0].desktop).toEqual({ agents: [], layoutTree: null });
    expect(useSessionStore.getState().sessions[0].daemonActivePaneId).toBe('');
  });

  it('syncFromDaemonSessions removes a closed session', () => {
    useSessionStore.setState({
      sessions: [
        {
          id: 'root-session',
          label: 'Root',
          state: 'idle',
          cwd: '/tmp/project',
          profileId: '',
          desktopId: 'desktop-root',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-split',
          desktop: {
            agents: [
              { id: 'pane-root', runtimeId: 'root-session', title: 'Root', sessionId: 'root-session' },
              { id: 'pane-split', runtimeId: 'split-session', title: 'Split', sessionId: 'split-session' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-root' },
                { type: 'pane', paneId: 'pane-split' },
              ],
            },
          },
        },
        {
          id: 'split-session',
          label: 'Split',
          state: 'idle',
          cwd: '/tmp/project',
          profileId: '',
          desktopId: 'desktop-root',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-split',
          desktop: {
            agents: [
              { id: 'pane-root', runtimeId: 'root-session', title: 'Root', sessionId: 'root-session' },
              { id: 'pane-split', runtimeId: 'split-session', title: 'Split', sessionId: 'split-session' },
            ],
            layoutTree: {
              type: 'split',
              splitId: 'root',
              direction: 'vertical',
              ratio: 0.5,
              children: [
                { type: 'pane', paneId: 'pane-root' },
                { type: 'pane', paneId: 'pane-split' },
              ],
            },
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([
      {
        id: 'root-session',
        label: 'Root',
        agent: 'shell',
        directory: '/tmp/project',
        state: 'idle',
      },
    ]);

    const state = useSessionStore.getState();
    expect(state.sessions.map((session) => session.id)).toEqual(['root-session']);
  });

  it('syncFromDaemonSessions puts a session that comes back on the desktop it is placed on', () => {
    const daemonSession = {
      id: 'blip-session',
      label: 'Blip',
      agent: 'codex',
      directory: '/tmp/project',
      profile_id: 'profile',
      state: 'working',
    };
    const desktop = desktopWith('desktop-blip', JSON.stringify({ type: 'pane', pane_id: 'pane-blip' }), 'pane-blip', [
      { pane_id: 'pane-blip', session_id: 'blip-session', title: 'Blip' },
    ]);

    useSessionStore.getState().syncFromDaemonSessions([daemonSession]);
    useSessionStore.getState().syncFromArrangement('profile', null, [desktop], { kind: 'scope' });
    expect(
      useSessionStore.getState().sessions.find((entry) => entry.id === 'blip-session')?.desktop.agents,
    ).toHaveLength(1);

    useSessionStore.getState().syncFromDaemonSessions([]);
    useSessionStore.getState().syncFromDaemonSessions([daemonSession]);

    const restored = useSessionStore.getState().sessions.find((entry) => entry.id === 'blip-session');
    expect(restored).toMatchObject({ desktopId: 'desktop-blip', profileId: 'profile', daemonActivePaneId: 'pane-blip' });
    expect(restored?.desktop.agents).toEqual([
      { id: 'pane-blip', runtimeId: 'blip-session', sessionId: 'blip-session', title: 'Blip' },
    ]);
  });

  it('syncFromDaemonSessions retains a genuinely launching session with a pending desktop pane', () => {
    useSessionStore.setState({
      sessions: [
        {
          id: 'launching-session',
          label: 'Launching',
          state: 'launching',
          cwd: '/tmp/launching',
          profileId: '',
          desktopId: 'desktop-launching',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-launching',
          desktop: {
            agents: [{
              id: 'pane-launching',
              runtimeId: 'launching-session',
              title: 'Launching',
              sessionId: 'launching-session',
              status: 'spawning',
            }],
            layoutTree: { type: 'pane', paneId: 'pane-launching' },
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([]);

    expect(useSessionStore.getState().sessions.map((session) => session.id)).toEqual(['launching-session']);
  });

  it('syncFromDaemonSessions keeps the session being created', async () => {
    const neighbour = {
      id: 'sess-neighbour',
      label: 'Neighbour',
      agent: 'shell',
      directory: '/tmp/neighbour',
      state: 'idle',
    };
    useSessionStore.getState().syncFromDaemonSessions([neighbour]);

    const sessionId = await useSessionStore.getState().createSession(
      'Racey', '/tmp/racey', 'sess-racey', 'claude', undefined, false,
    );

    useSessionStore.getState().syncFromDaemonSessions([neighbour]);

    const kept = useSessionStore.getState().sessions.find((s) => s.id === sessionId);
    expect(kept?.state).toBe('launching');

    const args = useSessionStore.getState().takeSessionSpawnArgs(sessionId, 80, 24);
    expect(args?.id).toBe(sessionId);
    expect(args?.cwd).toBe('/tmp/racey');
  });

  it('syncFromDaemonSessions prunes a created session once the daemon has reported it and dropped it', async () => {
    const sessionId = await useSessionStore.getState().createSession(
      'Racey', '/tmp/racey', 'sess-racey', 'claude', undefined, false,
    );

    useSessionStore.getState().syncFromDaemonSessions([{
      id: sessionId,
      label: 'Racey',
      agent: 'claude',
      directory: '/tmp/racey',
      state: 'idle',
    }]);
    expect(useSessionStore.getState().sessions.find((s) => s.id === sessionId)?.creating).toBeUndefined();

    useSessionStore.getState().syncFromDaemonSessions([]);

    expect(useSessionStore.getState().sessions).toEqual([]);
  });

  it('syncFromDaemonSessions removes an exited session with a stale spawning pane', () => {
    useSessionStore.setState({
      sessions: [
        {
          id: 'exited-session',
          label: 'Exited shell',
          state: 'idle',
          cwd: '/tmp/exited',
          profileId: '',
          desktopId: 'desktop-exited',
          agent: 'shell',
          transcriptMatched: true,
          daemonActivePaneId: 'pane-exited',
          desktop: {
            agents: [{
              id: 'pane-exited',
              runtimeId: 'exited-session',
              title: 'Exited shell',
              sessionId: 'exited-session',
              status: 'spawning',
            }],
            layoutTree: { type: 'pane', paneId: 'pane-exited' },
          },
        },
      ],
    });

    useSessionStore.getState().syncFromDaemonSessions([]);

    expect(useSessionStore.getState().sessions).toEqual([]);
  });

  it('takeSessionSpawnArgs applies launcher overrides', async () => {
    const sessionId = await useSessionStore.getState().createSession('Spawn Test', '/tmp/project', 'sess-spawn', 'claude', 'ep-1', true);
    useSessionStore.getState().setLauncherConfig({
      executables: { claude: '/opt/bin/claude-custom' },
    });

    const first = useSessionStore.getState().takeSessionSpawnArgs(sessionId, 120, 40);

    expect(first).toMatchObject({
      id: sessionId,
      cwd: '/tmp/project',
      endpoint_id: 'ep-1',
      label: 'Spawn Test',
      cols: 120,
      rows: 40,
      agent: 'claude',
      executable: '/opt/bin/claude-custom',
      claude_executable: '/opt/bin/claude-custom',
      resume_session_id: null,
      yolo_mode: true,
    });
  });

  it('syncFromArrangement gives each session the desktop it is placed on and that desktop\'s active pane', async () => {
    const sessionId = await useSessionStore.getState().createSession('Desktop', '/tmp/project', 'sess-desktop', 'codex', undefined, false);

    useSessionStore.getState().syncFromArrangement('profile', null, [
      desktopWith(
        'desktop-1',
        JSON.stringify({
          type: 'split',
          split_id: 'root',
          direction: 'vertical',
          ratio: 0.5,
          children: [
            { type: 'pane', pane_id: 'pane-session' },
            { type: 'pane', pane_id: 'pane-shell' },
          ],
        }),
        'pane-shell',
        [
          { pane_id: 'pane-session', session_id: sessionId, title: 'Agent' },
          { pane_id: 'pane-shell', session_id: 'sess-shell', title: 'Shell 1' },
        ],
      ),
    ], { kind: 'scope' });

    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);
    expect(session?.desktopId).toBe('desktop-1');
    expect(session?.desktop).toEqual({
      agents: [
        { id: 'pane-session', runtimeId: sessionId, sessionId, title: 'Agent' },
        { id: 'pane-shell', runtimeId: 'sess-shell', sessionId: 'sess-shell', title: 'Shell 1' },
      ],
      layoutTree: {
        type: 'split',
        splitId: 'root',
        direction: 'vertical',
        ratio: 0.5,
        ratioMode: 'automatic',
        children: [
          { type: 'pane', paneId: 'pane-session' },
          { type: 'pane', paneId: 'pane-shell' },
        ],
      },
    });
    expect(session?.daemonActivePaneId).toBe('pane-shell');
  });

  it('syncFromArrangement keeps defaults on an invalid tree and falls back to the first pane', async () => {
    const sessionId = await useSessionStore.getState().createSession('Desktop', '/tmp/project', 'sess-desktop', 'codex', undefined, false);

    useSessionStore.getState().syncFromArrangement('profile', null, [
      desktopWith('desktop-1', '{not-json', 'missing-pane', [
        { pane_id: 'pane-session', session_id: sessionId, title: 'Agent' },
      ]),
      desktopWith('desktop-2', '', 'pane-x', [{ pane_id: 'pane-x', session_id: 'unknown-session', title: 'Shell X' }]),
    ], { kind: 'scope' });

    const session = useSessionStore.getState().sessions.find((entry) => entry.id === sessionId);
    expect(session?.desktop).toEqual({
      agents: [{ id: 'pane-session', runtimeId: sessionId, sessionId, title: 'Agent' }],
      layoutTree: null,
    });
    expect(session?.daemonActivePaneId).toBe('pane-session');
    expect(useSessionStore.getState().sessions.map((entry) => entry.id)).toEqual([sessionId]);
  });

  it('keeps a failed desktop pane out of the session collection across snapshot orderings', () => {
    const failed = desktopWith('desktop-failed', JSON.stringify({ type: 'pane', pane_id: 'pane-failed' }), 'pane-failed', [
      {
        pane_id: 'pane-failed',
        session_id: 'closed-session',
        title: 'Failed reviewer',
        status: LayoutPaneStatus.Failed,
        error: 'session is closing',
      },
    ]);

    useSessionStore.getState().syncFromArrangement('profile', null, [failed], { kind: 'scope' });
    useSessionStore.getState().syncFromDaemonSessions([]);
    useSessionStore.getState().syncFromArrangement('profile', null, [failed], { kind: 'scope' });

    const state = useSessionStore.getState();
    expect(state.sessions).toEqual([]);
    expect(state.desktopSnapshots['desktop-failed']?.terminalState.agents).toEqual([{
      id: 'pane-failed',
      runtimeId: 'closed-session',
      sessionId: 'closed-session',
      title: 'Failed reviewer',
      status: 'failed',
      error: 'session is closing',
    }]);
  });

  it('reloadSession asks the daemon to reload with clamped geometry', async () => {
    await useSessionStore.getState().createSession('Remote', '/srv/repo', 'sess-remote', 'codex', 'ep-remote', true);

    await useSessionStore.getState().reloadSession('sess-remote', { cols: 12, rows: 40 });

    expect(mockPtyReload).toHaveBeenCalledWith({ id: 'sess-remote', cols: 80, rows: 24 });
  });

  it('propagates a failed reload to its caller', async () => {
    await useSessionStore.getState().createSession('Local', '/srv/repo', 'sess-reload-fail', 'codex', undefined, false);
    mockPtyReload.mockRejectedValueOnce(new Error('reload failed'));
    await expect(useSessionStore.getState().reloadSession('sess-reload-fail', { cols: 120, rows: 40 })).rejects.toThrow('reload failed');
  });
});
