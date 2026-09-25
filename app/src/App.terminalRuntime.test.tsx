// @ts-expect-error -- @types/node is not an app dependency
import { readFileSync } from 'node:fs';
import { act, fireEvent, screen } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { LOCAL_SNAPSHOT_FORMAT } from './pty/attachPlanning';
import { agentPane, agentWorkspace, daemonSession, daemonWorkspace, type DaemonSession } from './test/daemonFixtures';
import { pressShortcut, renderApp } from './test/renderApp';
import { initialState } from './test/scriptedDaemon';
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
  const measured = (axis: 'clientWidth' | 'clientHeight', size: number) => {
    const native = Object.getOwnPropertyDescriptor(HTMLElement.prototype, axis)!;
    Object.defineProperty(HTMLElement.prototype, axis, {
      configurable: true,
      get(this: HTMLElement) {
        return this.classList.contains('terminal-container') ? size : native.get!.call(this);
      },
    });
    onTestFinished(() => {
      Object.defineProperty(HTMLElement.prototype, axis, native);
    });
  };
  measured('clientWidth', width);
  measured('clientHeight', height);
}

describe('App terminal runtime', () => {
  it('restores the daemon snapshot before output that raced the attach', async () => {
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    open('s1');
    await daemon.idle();

    daemon.emit({ event: 'pty_output', id: 's1', seq: 11, data: btoa('live-after-snapshot') });
    daemon.emit({
      event: 'attach_result',
      id: 's1',
      success: true,
      cols: 40,
      rows: 6,
      last_seq: 10,
      running: true,
      snapshot: {
        cols: 40,
        rows: 6,
        snapshot_b64: btoa(Array.from(NATIVE_SNAPSHOT, (byte) => String.fromCharCode(byte)).join('')),
        format: LOCAL_SNAPSHOT_FORMAT,
        scrollback_truncated: false,
      },
    });
    await daemon.idle();

    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')).toBe([
      'row-1199 tail',
      'row-1200 tail',
      'STYLED',
      'wrapwrapwrapwrapwrapwrapwrapwrapwrapwrap',
      'wrapwrapwrapwrapwrap',
      'prompt$ live-after-snapshot',
    ].join('\n'));
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

  it('omits pixel geometry from the resize that reconciles a daemon answering the attach at another size', async () => {
    layOutTerminals(800, 600);
    const { daemon } = await renderSessions(daemonSession('s1', { state: 'idle' }));
    daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true }));

    open('s1');
    await daemon.idle();

    const afterAttach = daemon.sent.slice(daemon.sent.findIndex(({ cmd }) => cmd === 'attach_session'));
    expect(afterAttach.filter(({ cmd }) => cmd === 'pty_resize')).toEqual([{ cmd: 'pty_resize', id: 's1', cols: 100, rows: 28 }]);
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
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')).toContain('fresh-output');
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')).not.toContain('stale-output');
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
});
