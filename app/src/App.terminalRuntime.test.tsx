// @ts-expect-error -- @types/node is not an app dependency
import { readFileSync } from 'node:fs';
import { act, fireEvent, screen } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { LOCAL_SNAPSHOT_FORMAT } from './pty/attachPlanning';
import { openAttachedTerminals } from './test/appFixtures';
import { agentPane, agentWorkspace, daemonSession, daemonWorkspace, splitWorkspace, type DaemonSession } from './test/daemonFixtures';
import { fakeRects, sizeTerminals } from './test/layout';
import { pressShortcut, renderApp } from './test/renderApp';
import type { CommandMessage } from './test/protocol';
import { initialState, type ScriptedDaemon } from './test/scriptedDaemon';
import { WORKSPACE_RESIZE_COALESCE_MS } from './utils/ghosttyResize';
import { WARM_WORKSPACE_LIMIT_STORAGE_KEY } from './utils/terminalVirtualization';

const NATIVE_SNAPSHOT: Uint8Array = readFileSync('src/ghostty/testdata/native-snapshot.bin');

function renderSessions(...sessions: DaemonSession[]) {
  return renderApp({
    initialState: { sessions, workspaces: sessions.map((session) => agentWorkspace(session.id)) },
  });
}

function open(label: string) {
  fireEvent.click(screen.getByRole('button', { name: `Open ${label}` }));
}

function layOutTerminals(width: number, height: number) {
  const size = { clientWidth: width, clientHeight: height };
  sizeTerminals(() => size);
  return size;
}

function observeTerminalResizes() {
  const observers: Array<{ callback: ResizeObserverCallback; targets: Element[] }> = [];
  const browserResizeObserver = globalThis.ResizeObserver;
  globalThis.ResizeObserver = class {
    private readonly observer: { callback: ResizeObserverCallback; targets: Element[] };
    constructor(callback: ResizeObserverCallback) {
      this.observer = { callback, targets: [] };
      observers.push(this.observer);
    }
    observe(target: Element) {
      this.observer.targets.push(target);
    }
    unobserve() {}
    disconnect() {
      this.observer.targets = [];
    }
  };
  onTestFinished(() => {
    globalThis.ResizeObserver = browserResizeObserver;
  });
  return () => act(() => {
    for (const { callback, targets } of observers) {
      if (targets.some((target) => target.classList.contains('terminal-container'))) callback([], {} as ResizeObserver);
    }
  });
}

function resizableTerminals(width: number, height: number) {
  const pane = layOutTerminals(width, height);
  const observe = observeTerminalResizes();
  return async (nextWidth: number, nextHeight: number) => {
    pane.clientWidth = nextWidth;
    pane.clientHeight = nextHeight;
    observe();
    await act(() => vi.advanceTimersByTimeAsync(WORKSPACE_RESIZE_COALESCE_MS));
  };
}

function snapshotOf(bytes: Uint8Array = NATIVE_SNAPSHOT, format: string | null = LOCAL_SNAPSHOT_FORMAT) {
  return {
    cols: 40,
    rows: 6,
    snapshot_b64: btoa(Array.from(bytes, (byte) => String.fromCharCode(byte)).join('')),
    ...(format ? { format } : {}),
    scrollback_truncated: false,
  };
}

function snapshotReply(id: string, snapshot = snapshotOf()) {
  return { event: 'attach_result' as const, id, success: true, cols: 40, rows: 6, last_seq: 10, running: true, snapshot };
}

function visibleText(sessionId: string) {
  return window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.(sessionId).trim();
}

function resizesAfterAttach(daemon: ScriptedDaemon, attachIndex = 0) {
  const attaches = daemon.sent.flatMap((command, index) => (command.cmd === 'attach_session' ? [index] : []));
  return daemon.sent.slice(attaches[attachIndex]).filter((command) => command.cmd === 'pty_resize') as CommandMessage<'pty_resize'>[];
}

function attachesAndDetaches(daemon: ScriptedDaemon) {
  return daemon.sent.filter((command) => command.cmd === 'attach_session' || command.cmd === 'detach_session');
}

const RESTORED_SCREEN = [
  'row-1199 tail',
  'row-1200 tail',
  'STYLED',
  'wrapwrapwrapwrapwrapwrapwrapwrapwrapwrap',
  'wrapwrapwrapwrapwrap',
  'prompt$',
].join('\n');

async function openSplit(script: (daemon: ScriptedDaemon) => void = () => {}) {
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1', { state: 'idle', workspace_id: 'ws' }), daemonSession('s2', { state: 'idle', workspace_id: 'ws' })],
      workspaces: [splitWorkspace('ws', ['s1', 's2'])],
    },
  });
  script(view.daemon);
  open('s1');
  await view.daemon.idle();
  return view;
}

async function reattachAfterReconnect(output: string) {
  const view = await openAttachedTerminals({
    sessions: [daemonSession('s1', { state: 'idle' })],
    workspaces: [agentWorkspace('s1')],
    output: { s1: output },
  });
  view.daemon.on('attach_session', () => undefined);
  const connection = await view.daemon.reconnect();
  return { ...view, connection };
}

describe('App terminal runtime', () => {
  it('restores the daemon snapshot before output that raced the attach', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    open('s1');
    await daemon.idle();

    daemon.emit({ event: 'pty_output', id: 's1', seq: 11, data: btoa('live-after-snapshot') });
    daemon.emit(snapshotReply('s1'));
    await daemon.idle();

    expect(visibleText('s1')).toBe(`${RESTORED_SCREEN} live-after-snapshot`);
  });

  it.each([
    ['a snapshot it cannot decode at all', new Uint8Array([1, 2, 3, 4]), 'raced\nlive'],
    ['a snapshot cut off inside its history', NATIVE_SNAPSHOT.slice(0, NATIVE_SNAPSHOT.length - 1000), `${RESTORED_SCREEN} live`],
  ])('keeps what it restored from %s and shows live output, without attaching again', async (_, bytes, shown) => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    open('s1');
    await daemon.idle();

    daemon.emit({ event: 'pty_output', id: 's1', seq: 9, data: btoa('raced\r\n') });
    daemon.emit(snapshotReply('s1', snapshotOf(bytes)));
    await daemon.idle();
    daemon.emit({ event: 'pty_output', id: 's1', seq: 11, data: btoa('live') });
    await daemon.idle();

    expect(visibleText('s1')).toBe(shown);
    expect(daemon.sentOf('attach_session')).toHaveLength(1);
  });

  it('resizes a measured pane before re-attaching its runtime', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));

    open('s1');
    await daemon.idle();

    expect(daemon.sent.filter((command) => command.cmd === 'pty_resize' || command.cmd === 'attach_session')).toEqual([
      { cmd: 'pty_resize', id: 's1', cols: 100, rows: 28, xpixel: 800, ypixel: 588 },
      { cmd: 'attach_session', id: 's1', attach_policy: 'same_app_remount' },
    ]);
  });

  it('reports the pane size in device pixels on a high-density display', async () => {
    layOutTerminals(800, 600);
    vi.stubGlobal('devicePixelRatio', 2);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));

    open('s1');
    await daemon.idle();

    expect(daemon.sentOf('pty_resize')).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 100, rows: 28, xpixel: 1600, ypixel: 1176 }]);
  });

  it('measures a resized pane after layout, and folds a burst of resizes into one once they settle', async () => {
    const pane = layOutTerminals(800, 600);
    const observe = observeTerminalResizes();
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 100, rows: 28, running: true }));
    open('s1');
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(300));
    const resizesBefore = daemon.sentOf('pty_resize').length;
    const resizes = () => daemon.sentOf('pty_resize').slice(resizesBefore);

    pane.clientWidth = 360;
    observe();
    expect(resizes()).toEqual([]);
    pane.clientWidth = 440;
    await act(() => vi.advanceTimersToNextFrame());
    expect(resizes()).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 55, rows: 28, xpixel: 440, ypixel: 588 }]);

    for (const width of [480, 560]) {
      pane.clientWidth = width;
      observe();
      await act(() => vi.advanceTimersToNextFrame());
    }
    expect(resizes()).toHaveLength(1);
    await act(() => vi.advanceTimersByTimeAsync(250));
    expect(resizes()).toEqual([
      { cmd: 'pty_resize', id: 's1', cols: 55, rows: 28, xpixel: 440, ypixel: 588 },
      { cmd: 'pty_resize', id: 's1', cols: 70, rows: 28, xpixel: 560, ypixel: 588 },
    ]);
  });

  it('keeps wrapping output at the old width until the daemon’s resize arrives in the stream, without reflowing what came before', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    open('s1');
    await daemon.idle();
    expect(daemon.sentOf('pty_resize').slice(-1)[0]).toEqual({ cmd: 'pty_resize', id: 's1', cols: 100, rows: 28 });

    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa(`${'A'.repeat(90)}\r\n`) });
    daemon.emit({ event: 'pty_resized', id: 's1', cols: 100, rows: 28 });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa(`${'B'.repeat(90)}\r\n`) });
    await daemon.idle();

    expect(visibleText('s1')).toBe(['A'.repeat(80), 'A'.repeat(10), 'B'.repeat(90)].join('\n'));
  });

  it('leaves wraparound off across a resize when the program turned it off', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    open('s1');
    await daemon.idle();

    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('\x1b[?7l') });
    daemon.emit({ event: 'pty_resized', id: 's1', cols: 90, rows: 24 });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('D'.repeat(120)) });
    await daemon.idle();

    expect(visibleText('s1')).toBe('D'.repeat(90));
  });

  it('omits pixel geometry from the resize that reconciles a daemon answering the attach at another size', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));

    open('s1');
    await daemon.idle();

    expect(resizesAfterAttach(daemon)).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 100, rows: 28 }]);
  });

  it('revives a recoverable session with its geometry in the attach itself', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'recoverable' }));

    open('s1');
    await daemon.idle();

    expect(daemon.sent.filter((command) => command.cmd === 'pty_resize' || command.cmd === 'attach_session')).toEqual([
      { cmd: 'attach_session', id: 's1', attach_policy: 'revive', cols: 100, rows: 28 },
    ]);
  });

  it('drops an attach answered after the user left, and attaches afresh on return', async () => {
    localStorage.setItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY, '0');
    onTestFinished(() => localStorage.removeItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY));
    const sessions = [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })];
    const { daemon } = await renderSessions(...sessions);
    open('s1');
    await daemon.idle();
    open('s2');
    await daemon.idle();

    daemon.emit({ event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, last_seq: 0, running: true });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('stale-output') });
    daemon.emit(initialState({ sessions, workspaces: sessions.map((session) => agentWorkspace(session.id)) }));
    await daemon.idle();
    open('s1');
    await daemon.idle();

    expect(daemon.sent.filter((command) => command.cmd === 'attach_session' || command.cmd === 'detach_session')).toEqual([
      { cmd: 'attach_session', id: 's1', attach_policy: 'same_app_remount' },
      { cmd: 'detach_session', id: 's1' },
      { cmd: 'attach_session', id: 's2', attach_policy: 'same_app_remount' },
      { cmd: 'detach_session', id: 's2' },
      { cmd: 'attach_session', id: 's1', attach_policy: 'same_app_remount' },
    ]);

    daemon.emit({ event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, last_seq: 1, running: true });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('fresh-output') });
    await daemon.idle();
    expect(visibleText('s1')).toContain('fresh-output');
    expect(visibleText('s1')).not.toContain('stale-output');
  });

  it('shows each session’s output only in its own pane', async () => {
    const { daemon } = await openSplit((script) => {
      script.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    });

    daemon.emit({ event: 'pty_output', id: 's2', seq: 1, data: btoa('only-in-s2') });
    await daemon.idle();

    expect(visibleText('s2')).toContain('only-in-s2');
    expect(visibleText('s1')).not.toContain('only-in-s2');
  });

  it('answers a terminal query once while a session moves to another workspace, and keeps rendering it there', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    open('s1');
    await daemon.idle();
    open('s2');
    await daemon.idle();

    daemon.emit({
      event: 'workspace_layout_updated',
      workspace_layout: splitWorkspace('workspace-s2', ['s2', 's1']).layout!,
    });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('\x1b[5n') });
    await daemon.idle();
    daemon.emit({ event: 'session_state_changed', session: daemonSession('s1', { state: 'idle', workspace_id: 'workspace-s2' }) });
    await daemon.idle();
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('after-the-move\x1b[5n') });
    await daemon.idle();

    expect(daemon.sentOf('pty_input')).toEqual([
      { cmd: 'pty_input', id: 's1', data: '\x1b[0n', source: 'response' },
      { cmd: 'pty_input', id: 's1', data: '\x1b[0n', source: 'response' },
    ]);
    expect(visibleText('s1')).toContain('after-the-move');
  });

  it('spawns a split shell in its workspace and leaves attaching it to its pane', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('workspace_layout_add_session_pane', ({ workspace_id, pane_id, session_id }) => [
      { event: 'workspace_layout_action_result', action: 'workspace_layout_add_session_pane', workspace_id, pane_id, success: true },
      {
        event: 'workspace_layout_updated',
        workspace_layout: daemonWorkspace(workspace_id, {
          root: {
            type: 'split',
            split_id: 'split-shell',
            direction: 'vertical',
            ratio: 0.5,
            children: [{ type: 'pane', pane_id: 'pane-s1' }, { type: 'pane', pane_id }],
          },
          panes: [agentPane('s1', workspace_id), { ...agentPane(session_id!, workspace_id), pane_id: pane_id! }],
        }).layout!,
      },
    ]);
    daemon.on('spawn_session', ({ id }) => [
      { event: 'spawn_result', id, success: true },
      { event: 'session_registered', session: daemonSession(id, { agent: 'shell', workspace_id: 'workspace-s1', state: 'idle' }) },
    ]);
    open('s1');
    await daemon.idle();

    pressShortcut('terminal.splitVertical');
    await daemon.idle();

    const [spawn] = daemon.sentOf('spawn_session');
    expect(spawn).toMatchObject({ cwd: '/tmp/s1', workspace_id: 'workspace-s1', agent: 'shell', spawned_from: 's1' });
    expect(daemon.sent.filter((command) =>
      (command.cmd === 'attach_session' || command.cmd === 'pty_resize') && command.id === spawn.id,
    )).toEqual([{ cmd: 'attach_session', id: spawn.id, attach_policy: 'same_app_remount' }]);
  });

  it('reloads a session from its actions menu and says when the daemon refuses', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    let refusal: string | undefined;
    daemon.on('reload_session', ({ id }) => (
      refusal
        ? { event: 'reload_session_result', id, success: false, error: refusal }
        : { event: 'reload_session_result', id, success: true }
    ));
    const reload = async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Actions for s1' }));
      fireEvent.click(screen.getByRole('menuitem', { name: /Reload session/ }));
      await daemon.idle();
    };

    await reload();
    expect(daemon.sentOf('reload_session')).toEqual([{ cmd: 'reload_session', id: 's1', cols: 80, rows: 24 }]);
    expect(screen.queryByText(/Failed to reload session/)).toBeNull();

    refusal = 'reload denied';
    await reload();
    expect(screen.getByText('Failed to reload session: reload denied')).toBeInTheDocument();
  });

  it('reports pointer activity over a terminal only once the daemon handshake completes', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    open('s1');
    await daemon.idle();
    const moveOverTerminal = async () => {
      await act(() => vi.advanceTimersByTimeAsync(250));
      fireEvent.mouseMove(screen.getByRole('textbox', { name: 'Terminal input' }));
      await daemon.idle();
    };
    daemon.on('client_hello', () => undefined);

    const reconnected = await daemon.reconnect();
    await moveOverTerminal();
    expect(reconnected.sent.filter((command) => command.cmd === 'terminal_pointer_activity')).toEqual([]);

    reconnected.emit(initialState({
      sessions: [daemonSession('s1', { state: 'idle' })],
      workspaces: [agentWorkspace('s1')],
    }));
    await moveOverTerminal();
    expect(reconnected.sent.filter((command) => command.cmd === 'terminal_pointer_activity')).toEqual([
      { cmd: 'terminal_pointer_activity', id: 's1' },
    ]);
  });

  it('drives the in-app browser for the daemon and returns what it did', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockImplementation(async (command: string) => {
      if (command === 'get_browser_host_token') return 'browser-token';
      if (command === 'browser_host_control') return '{"title":"Fixture"}';
      return undefined;
    });
    const { daemon } = await renderApp();
    expect(daemon.sentOf('client_hello')[0]).toMatchObject({
      capabilities: expect.arrayContaining(['browser_host']),
      browser_host_token: 'browser-token',
    });

    daemon.emit({
      event: 'browser_control_request',
      request_id: 'browser-request-1',
      workspace_id: 'workspace-1',
      tile_id: 'tile-browser',
      action: 'type',
      selector: '#query',
      text: 'browser text',
    });

    expect(await daemon.received('browser_control_result')).toEqual({
      cmd: 'browser_control_result',
      request_id: 'browser-request-1',
      success: true,
      data: '{"title":"Fixture"}',
    });
    expect(vi.mocked(invoke)).toHaveBeenCalledWith('browser_host_control', {
      label: 'browser-workspace-1-tile-browser',
      action: 'type',
      selector: '#query',
      text: 'browser text',
    });
  });

  it('answers a browser request whose result would overflow the daemon connection with a failure', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockImplementation(async (command: string) => {
      if (command === 'browser_host_control') return '"'.repeat(12_600_000);
      return undefined;
    });
    const { daemon } = await renderApp();

    daemon.emit({
      event: 'browser_control_request',
      request_id: 'browser-request-1',
      workspace_id: 'workspace-1',
      tile_id: 'tile-browser',
      action: 'evaluate',
    });

    expect(await daemon.received('browser_control_result')).toEqual({
      cmd: 'browser_control_result',
      request_id: 'browser-request-1',
      success: false,
      error: expect.stringMatching(/^Error: serialized browser control result is 25200\d+ bytes; the maximum supported result is 25165824 bytes$/),
    });
  });

  it('places the native browser over its tile and follows the tile when the window resizes', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    let hostRect = new DOMRect(10, 20, 300, 400);
    fakeRects((element) => (element.classList.contains('browser-tile-host') ? hostRect : null));
    const workspace = daemonWorkspace('ws', {
      root: {
        type: 'split',
        split_id: 'split-a',
        direction: 'vertical',
        ratio: 0.5,
        children: [
          { type: 'pane', pane_id: 'pane-s1' },
          { type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://example.com/' },
        ],
      },
      panes: [agentPane('s1', 'ws')],
    }, { title: 'ws' });
    const { daemon } = await renderApp({
      initialState: { sessions: [daemonSession('s1', { workspace_id: 'ws', state: 'idle' })], workspaces: [workspace] },
    });
    open('s1');
    await daemon.idle();

    expect(vi.mocked(invoke)).toHaveBeenCalledWith('browser_host_mount', {
      label: 'browser-ws-tile-browser',
      url: 'https://example.com/',
      geometry: { x: 10, y: 20, width: 300, height: 400, visible: false },
    });
    expect(vi.mocked(invoke)).toHaveBeenLastCalledWith('browser_host_update', {
      label: 'browser-ws-tile-browser',
      geometry: { x: 10, y: 20, width: 300, height: 400, visible: true },
    });

    hostRect = new DOMRect(30, 40, 500, 600);
    fireEvent(window, new Event('resize'));
    await daemon.idle();

    expect(vi.mocked(invoke)).toHaveBeenLastCalledWith('browser_host_update', {
      label: 'browser-ws-tile-browser',
      geometry: { x: 30, y: 40, width: 500, height: 600, visible: true },
    });
  });

  it('answers terminal queries the daemon leaves to the app, and not the ones the daemon answers', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: [daemonSession('s1', { state: 'idle' })],
      workspaces: [agentWorkspace('s1')],
    });

    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('\x1b[6n\x1b[5n') });
    await daemon.idle();

    expect(daemon.sentOf('pty_input')).toEqual([{ cmd: 'pty_input', id: 's1', data: '\x1b[0n', source: 'response' }]);
  });

  it('holds layout resizes until the attach lands, then sends only the latest size', async () => {
    const resize = resizableTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    open('s1');
    await daemon.idle();

    await resize(1000, 600);
    await resize(1200, 600);
    await daemon.idle();
    expect(daemon.sentOf('pty_resize')).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 100, rows: 28, xpixel: 800, ypixel: 588 }]);

    daemon.emit({ event: 'attach_result', id: 's1', success: true, cols: 100, rows: 28, running: true });
    await daemon.idle();
    expect(resizesAfterAttach(daemon)).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 150, rows: 28, xpixel: 1200, ypixel: 588 }]);
  });

  it('follows a pane to the PTY however short it gets, but not a pane laid out to nothing', async () => {
    const resize = resizableTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 100, rows: 28, running: true }));
    open('s1');
    await daemon.idle();

    await resize(800, 100);
    await resize(0, 0);
    await daemon.idle();

    expect(resizesAfterAttach(daemon)).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 100, rows: 4, xpixel: 800, ypixel: 84 }]);
  });

  it('restores a snapshot at its own grid, then fits the PTY to the shown pane', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => snapshotReply(id));

    open('s1');
    await daemon.idle();

    expect(visibleText('s1')).toBe(RESTORED_SCREEN);
    expect(new Set(resizesAfterAttach(daemon).map(({ cols, rows }) => `${cols}x${rows}`))).toEqual(new Set(['100x28']));
  });

  it('only reconciles, without refitting, a snapshot restored after the user moved to another session', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' }));
    open('s1');
    await daemon.idle();
    open('s2');
    await daemon.idle();

    daemon.emit(snapshotReply('s1'));
    await daemon.idle();

    expect(visibleText('s1')).toBe(RESTORED_SCREEN);
    expect(resizesAfterAttach(daemon).filter(({ id }) => id === 's1')).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 100, rows: 28 }]);
  });

  it('keeps the daemon geometry on a same-size attach of a measured pane', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, running: true, cols: 100, rows: 28 }));

    open('s1');
    await daemon.idle();

    expect(resizesAfterAttach(daemon)).toEqual([]);
  });

  it('attaches a pane that was never measured without resizing the PTY', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, running: true, cols: 45, rows: 35 }));

    open('s1');
    await daemon.idle();

    expect(daemon.sentOf('attach_session')).toHaveLength(1);
    expect(daemon.sentOf('pty_resize')).toEqual([]);
  });

  it('keeps the daemon geometry when it restores a respawned runtime', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 100, rows: 28, running: true }));
    open('s1');
    await daemon.idle();
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 37, rows: 46, running: true }));

    daemon.emit({ event: 'runtime_respawned', id: 's1' });
    await daemon.idle();

    expect(daemon.sentOf('attach_session')[1]).toEqual({ cmd: 'attach_session', id: 's1', attach_policy: 'relaunch_restore' });
    expect(resizesAfterAttach(daemon, 1)).toEqual([]);
  });

  it('restores a snapshot without writing to the PTY, keeps its scrollback, and encodes keys in the modes set after it', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => snapshotReply(id));
    open('s1');
    await daemon.idle();
    expect(daemon.sentOf('pty_input')).toEqual([]);

    const terminal = screen.getByRole('textbox', { name: 'Terminal input' });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    fireEvent.wheel(terminal, { deltaY: -100000 });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(visibleText('s1')?.split('\n')[0]).toBe('row-0001 tail');

    daemon.emit({ event: 'pty_output', id: 's1', seq: 11, data: btoa('m\x1b[?1h') });
    await daemon.idle();
    fireEvent.keyDown(terminal, { key: 'ArrowUp', code: 'ArrowUp' });
    await daemon.idle();
    expect(daemon.sentOf('pty_input').map(({ data }) => data)).toEqual(['\x1bOA']);
  });

  it('finds text in a snapshot adopted while find is open', async () => {
    const { daemon, connection } = await reattachAfterReconnect('before\r\n');
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Terminal input' }), { key: 'f', metaKey: true });
    fireEvent.change(screen.getByTestId('ghostty-find-input'), { target: { value: 'STYLED' } });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(screen.getByTestId('ghostty-find-count')).toHaveTextContent('0/0');

    connection.emit(snapshotReply('s1'));
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(1000));

    expect(screen.getByTestId('ghostty-find-count')).toHaveTextContent('1/1');
  });

  it.each([
    ['an empty snapshot', snapshotOf(new Uint8Array())],
    ['a snapshot from another build', snapshotOf(NATIVE_SNAPSHOT, 'deadbeef1234')],
    ['a snapshot that names no format', snapshotOf(NATIVE_SNAPSHOT, null)],
    ['a snapshot it cannot decode', snapshotOf(new Uint8Array([0, 1, 2, 3, 4, 5, 6, 7]))],
    ['no snapshot', null],
  ])('keeps its own screen and the output queued behind a re-attach answered with %s', async (_, snapshot) => {
    const { daemon, connection } = await reattachAfterReconnect('before\r\n');

    connection.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('queued\r\n') });
    connection.emit(snapshot
      ? { ...snapshotReply('s1', snapshot), cols: 80, rows: 24, last_seq: 2 }
      : { event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, last_seq: 2, running: true });
    connection.emit({ event: 'pty_output', id: 's1', seq: 3, data: btoa('after') });
    await daemon.idle();

    expect(visibleText('s1')).toBe('before\nqueued\nafter');
  });

  it('paints replayed output once', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: [daemonSession('s1', { state: 'idle' })],
      workspaces: [agentWorkspace('s1')],
      output: { s1: 'one\r\n' },
    });

    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('one-replayed\r\n') });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('two') });
    await daemon.idle();

    expect(visibleText('s1')).toBe('one\ntwo');
  });

  it('keeps the newest output when more arrives than it buffers while an attach is pending', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    open('s1');
    await daemon.idle();

    for (let seq = 1; seq <= 520; seq += 1) {
      daemon.emit({ event: 'pty_output', id: 's1', seq, data: btoa(`chunk-${seq}\r\n`) });
    }
    daemon.emit({ event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, last_seq: 0, running: true });
    await daemon.idle();

    const lines = window.__TEST_GET_SESSION_PANE_TEXT?.('s1').split('\n').filter(Boolean);
    expect([lines?.[0], lines?.[lines.length - 1], lines?.length]).toEqual(['chunk-9', 'chunk-520', 512]);
  });

  it('re-attaches a pane whose terminal remounts when the maximized pane is restored', async () => {
    const { daemon } = await openSplit((script) => {
      script.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
    });

    pressShortcut('terminal.toggleMaximize');
    await daemon.idle();
    pressShortcut('terminal.toggleMaximize');
    await daemon.idle();

    expect(attachesAndDetaches(daemon)).toEqual([
      { cmd: 'attach_session', id: 's1', attach_policy: 'same_app_remount' },
      { cmd: 'attach_session', id: 's2', attach_policy: 'same_app_remount' },
      { cmd: 'attach_session', id: 's2', attach_policy: 'same_app_remount' },
    ]);
  });

  it('keeps streaming to a remounted pane whose re-attach failed', async () => {
    let s2Attaches = 0;
    const { daemon } = await openSplit((script) => {
      script.on('attach_session', ({ id }) => (
        id === 's2' && ++s2Attaches === 2
          ? { event: 'attach_result', id, success: false, error: 'daemon busy' }
          : { event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }
      ));
    });
    pressShortcut('terminal.toggleMaximize');
    await daemon.idle();
    pressShortcut('terminal.toggleMaximize');
    await daemon.idle();

    daemon.emit({ event: 'pty_output', id: 's2', seq: 1, data: btoa('still streaming') });
    await daemon.idle();

    expect(visibleText('s2')).toBe('[Failed to attach PTY: Error: daemon busy]\nstill streaming');
    expect(daemon.sentOf('detach_session')).toEqual([]);
  });

  it('shows a failed revive in the terminal and leaves retrying to the user', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'recoverable' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: false, error: 'session not found: s1' }));

    open('s1');
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(5000));
    await daemon.idle();

    expect(visibleText('s1')).toBe('[Failed to attach PTY: Error: session not found: s1]');
    expect(attachesAndDetaches(daemon)).toEqual([
      { cmd: 'attach_session', id: 's1', attach_policy: 'revive', cols: 80, rows: 24 },
      { cmd: 'detach_session', id: 's1' },
    ]);
  });

  it('keeps a session attached while another workspace still shows it, and detaches once the last view goes', async () => {
    const own = splitWorkspace('ws-a', ['s1']);
    const shared = splitWorkspace('ws-b', ['s2', 's1']);
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('s1', { state: 'idle', workspace_id: 'ws-a' }), daemonSession('s2', { state: 'idle', workspace_id: 'ws-b' })],
        workspaces: [own, shared],
      },
    });
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));
    open('s1');
    await daemon.idle();
    open('s2');
    await daemon.idle();
    expect(document.querySelectorAll('[data-pane-id="pane-s1"]')).toHaveLength(2);

    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('\x1b[5n') });
    await daemon.idle();
    expect(daemon.sentOf('pty_input').filter(({ id }) => id === 's1')).toEqual([{ cmd: 'pty_input', id: 's1', data: '\x1b[0n', source: 'response' }]);

    daemon.emit({ event: 'workspace_unregistered', workspace: shared });
    daemon.emit({ event: 'pty_output', id: 's1', seq: 2, data: btoa('still here') });
    await daemon.idle();
    expect(daemon.sentOf('detach_session').filter(({ id }) => id === 's1')).toEqual([]);
    expect(visibleText('s1')).toBe('still here');

    daemon.emit({ event: 'workspace_unregistered', workspace: own });
    await daemon.idle();
    expect(daemon.sentOf('detach_session').filter(({ id }) => id === 's1')).toEqual([{ cmd: 'detach_session', id: 's1' }]);
  });
});
