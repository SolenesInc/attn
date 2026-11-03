// @ts-expect-error -- @types/node is not an app dependency
import { readFileSync } from 'node:fs';
import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { LOCAL_SNAPSHOT_FORMAT } from './pty/attachPlanning';
import { openSession } from './test/appFixtures';
import { daemonSession, defaultProfile, soloDesktop, splitDesktop } from './test/daemonFixtures';
import { pane, relayOut, renderDesktop, split } from './test/desktopLayouts';
import { fakeRects } from './test/layout';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { CommandMessage } from './test/protocol';
import { initialState, type ScriptedDaemon } from './test/scriptedDaemon';

const NATIVE_SNAPSHOT: Uint8Array = readFileSync('src/ghostty/testdata/native-snapshot.bin');
const sharedHost = (enabled: boolean) => ({ settings: { pty_shared_host_enabled: String(enabled) } });

function streams(daemon: ScriptedDaemon, from = 0) {
  return daemon.sent.slice(from).flatMap((command) => {
    if (command.cmd === 'attach_session') return [`attach ${command.id}`];
    if (command.cmd === 'detach_session') return [`detach ${command.id}`];
    return [];
  });
}

const mounted = (sessionId: string) => document.querySelector(`[data-pane-id="pane-${sessionId}"] canvas`) !== null;

async function openDesktops(enabled: boolean, ids = ['s1', 's2', 's3']) {
  const view = await renderApp({
    initialState: {
      ...sharedHost(enabled),
      sessions: ids.map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ids.map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
    },
    script: (daemon) => {
      daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    },
  });
  await openSession(view.daemon, ids[0]);
  return view.daemon;
}

describe('streaming only the terminals the window draws', () => {
  it('detaches the previous desktop, keeps its terminal, and attaches it with replay on return', async () => {
    const daemon = await openDesktops(true);
    const start = daemon.sent.length;

    await openSession(daemon, 's2');
    expect(streams(daemon, start)).toEqual(['detach s1', 'attach s2']);
    expect(mounted('s1')).toBe(true);

    const back = daemon.sent.length;
    await openSession(daemon, 's1');
    expect(streams(daemon, back)).toEqual(['detach s2', 'attach s1']);
    expect(daemon.sentOf('attach_session').slice(-1)[0]).toEqual(expect.objectContaining({ id: 's1', attach_policy: 'same_app_remount' }));
  });

  it('shows the daemon screen, not the last frame, in a pane that streamed while undrawn', async () => {
    const daemon = await openDesktops(true);
    daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('before leaving') });
    await openSession(daemon, 's2');
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')).toContain('before leaving');

    daemon.on('attach_session', ({ id }) => ({
      event: 'attach_result', id, success: true, cols: 40, rows: 6, running: true, last_seq: 7,
      snapshot: { cols: 40, rows: 6, snapshot_b64: btoa(Array.from(NATIVE_SNAPSHOT, (byte) => String.fromCharCode(byte)).join('')), format: LOCAL_SNAPSHOT_FORMAT, scrollback_truncated: false },
    }));
    await openSession(daemon, 's1');

    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')).toContain('row-1200 tail');
    expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('s1')).not.toContain('before leaving');
  });

  it('keeps the previous desktop attached while the shared terminal host is off', async () => {
    const daemon = await openDesktops(false);
    const start = daemon.sent.length;

    await openSession(daemon, 's2');

    expect(streams(daemon, start)).toEqual(['attach s2']);
  });

  it('detaches the drawn panes while the dashboard shows and attaches them on return', async () => {
    const daemon = await openDesktops(true);
    const start = daemon.sent.length;

    await gesture(daemon, () => pressShortcut('session.goToDashboard'));
    expect(streams(daemon, start)).toEqual(['detach s1']);
    expect(mounted('s1')).toBe(true);

    const back = daemon.sent.length;
    await openSession(daemon, 's1');
    expect(streams(daemon, back)).toEqual(['attach s1']);
  });

  it('detaches a pane hidden by a maximized pane and attaches it when restored', async () => {
    const { daemon } = await renderApp({
      initialState: { ...sharedHost(true), sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })), desktops: [splitDesktop('ws', ['s1', 's2'])] },
      script: (script) => script.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, cols: 80, rows: 24, running: true })),
    });
    await openSession(daemon, 's1');
    const start = daemon.sent.length;

    await gesture(daemon, () => pressShortcut('terminal.toggleMaximize'));
    expect(streams(daemon, start)).toEqual(['detach s2']);
    await gesture(daemon, () => pressShortcut('terminal.toggleMaximize'));
    expect(streams(daemon, start)).toEqual(['detach s2', 'attach s2']);
  });

  it.each([false, true])('detaches an attached leaf the layout suspends to a strip (shared host %s)', async (enabled) => {
    fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, 1200, 800) : null));
    const ids = ['s1', 's2', 's3', 's4'];
    const { daemon } = await renderDesktop(split('a', 'vertical', [pane('s1'), pane('s2')]), ids);
    if (enabled) daemon.emit({ event: 'settings_updated', settings: sharedHost(true).settings });
    await openSession(daemon, 's1');
    expect(streams(daemon)).toEqual(['attach s1', 'attach s2']);

    relayOut(daemon, split('a', 'vertical', [split('b', 'vertical', [pane('s1'), pane('s2')]), split('c', 'vertical', [pane('s3'), pane('s4')])]), ids, { active: 'pane-s4' });
    await act(() => vi.advanceTimersByTimeAsync(300));
    await daemon.idle();

    const suspended = ids.filter((id) => document.querySelector(`[data-pane-id="pane-${id}"]`)?.getAttribute('data-pane-suspended') === 'true');
    expect(suspended).toContain('s2');
    const attached = new Set<string>();
    for (const entry of streams(daemon)) {
      const [verb, id] = entry.split(' ');
      if (verb === 'attach') attached.add(id); else attached.delete(id);
    }
    expect([...attached].sort()).toEqual(ids.filter((id) => !suspended.includes(id)).sort());
  });

  it('re-attaches after a respawn or a reconnect only the terminals a drawn pane holds', async () => {
    const daemon = await openDesktops(true);
    await openSession(daemon, 's2');

    const start = daemon.sent.length;
    daemon.emit({ event: 'runtime_respawned', id: 's1' });
    daemon.emit({ event: 'runtime_respawned', id: 's3' });
    await daemon.idle();
    expect(streams(daemon, start)).toEqual([]);

    await daemon.reconnect();
    await daemon.idle();
    expect(daemon.sentOf('attach_session').map((command) => command.id).slice(-1)).toEqual(['s2']);
    expect(streams(daemon, start)).toEqual(['attach s2']);
  });

  it('does not re-attach a pane whose desync arrives after it stopped being drawn', async () => {
    const daemon = await openDesktops(true);
    await openSession(daemon, 's2');
    const start = daemon.sent.length;

    daemon.emit({ event: 'pty_desync', id: 's1', reason: 'stream_backpressure' });
    await daemon.idle();

    expect(streams(daemon, start)).toEqual([]);
  });

  it('attaches, after a reconnect, the pane the daemon now shows and not the one shown before', async () => {
    const daemon = await openDesktops(true, ['s1', 's2']);
    daemon.on('client_hello', () => initialState({
      settings: sharedHost(true).settings,
      sessions: ['s1', 's2'].map((id) => daemonSession(id, { state: 'idle' })),
      desktops: ['s1', 's2'].map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
      profiles: [defaultProfile('desktop-s2')],
    }));

    const reconnected = await daemon.reconnect();
    await daemon.idle();

    expect(reconnected.sent.filter((command) => command.cmd === 'attach_session').map(({ id }) => id)).toEqual(['s2']);
  });

  it('keeps holding keys for the newest attach when an older one for the same pane settles first', async () => {
    const daemon = await openDesktops(true, ['s1', 's2']);
    await openSession(daemon, 's2');
    const held: CommandMessage<'attach_session'>[] = [];
    daemon.on('attach_session', (command) => { held.push(command); return undefined; });

    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    const s1 = () => held.filter((command) => command.id === 's1');
    await gesture(daemon, () => daemon.replyTo(s1()[0], { event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    expect(s1()).toHaveLength(2);

    await gesture(daemon, () => fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' }));
    expect(daemon.sentOf('pty_input')).toEqual([]);

    await gesture(daemon, () => daemon.replyTo(s1()[1], { event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, running: true, last_seq: 0 }));
    expect(daemon.sentOf('pty_input')).toEqual([expect.objectContaining({ id: 's1', data: 'x' })]);
  });

  it('delivers keys typed while switching to an undrawn pane after its attach replay lands', async () => {
    const daemon = await openDesktops(true, ['s1', 's2']);
    await openSession(daemon, 's2');
    const held: CommandMessage<'attach_session'>[] = [];
    daemon.on('attach_session', (command) => { held.push(command); return undefined; });

    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => {
      fireEvent.keyDown(document.activeElement!, { key: 'x', code: 'KeyX' });
      fireEvent.keyUp(document.activeElement!, { key: 'x', code: 'KeyX' });
    });
    expect(held.map((command) => command.id)).toEqual(['s1']);
    expect(daemon.sentOf('pty_input')).toEqual([]);

    await gesture(daemon, () => {
      daemon.emit({ event: 'pty_output', id: 's1', seq: 1, data: btoa('\x1b[>31u') });
      daemon.replyTo(held[0], { event: 'attach_result', id: 's1', success: true, cols: 80, rows: 24, running: true, last_seq: 0 });
    });
    expect(daemon.sentOf('pty_input')).toEqual([
      expect.objectContaining({ id: 's1', data: '\x1b[120;;120u' }),
      expect.objectContaining({ id: 's1', data: '\x1b[120;1:3u' }),
    ]);
    expect(screen.queryByText(/could not be delivered/)).toBeNull();
  });
});
