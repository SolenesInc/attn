import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import {
  daemonSession,
  defaultProfile,
  soloDesktop,
  terminalDesktop,
  type DaemonDesktop,
  type DaemonSession,
} from './test/daemonFixtures';
import { gesture, renderApp } from './test/renderApp';
import { initialState, type ScriptedDaemon } from './test/scriptedDaemon';

const QUEUE = { queue_mode_enabled: 'true' };
const OWED = { turn_owed: true, turn_opened_at: '2026-08-03T09:00:00Z' };

const TERMINALS = [
  ['a terminal from before terminal ids, carrying its session id', 's1'],
  ['a terminal with its own id', 'terminal-1'],
];

function shownLeaf(): string | null {
  return document.querySelector('[data-session-visible="1"]')?.getAttribute('data-active-leaf-id') ?? null;
}

function shownDesktops() {
  return Array.from(document.querySelectorAll('.session-terminal-desktop[data-session-visible="1"]'))
    .map((desktop) => desktop.getAttribute('data-desktop-id'));
}

function focusedPane(): string | null {
  return document.activeElement?.closest('[data-pane-id]')?.getAttribute('data-pane-id') ?? null;
}

function attachesAndDetaches(daemon: ScriptedDaemon) {
  return daemon.sent.filter((command) => command.cmd === 'attach_session' || command.cmd === 'detach_session');
}

async function open(daemon: ScriptedDaemon, id: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: `Open ${id}` })));
}

// The predecessor's pane, now showing the successor in the same terminal.
function showing(successor: string, terminal: string): DaemonDesktop {
  const desktop = terminalDesktop('s1', terminal);
  return { ...desktop, revision: 2, panes: desktop.panes.map((pane) => ({ ...pane, session_id: successor })) };
}

function successorOf(predecessor: DaemonSession, successor: string, overrides: Partial<DaemonSession> = {}) {
  return daemonSession(successor, { state: 'idle', succeeds: predecessor.id, ...overrides });
}

// In the daemon's order: the successor, then the arrangement showing it, then the predecessor's end.
async function succeed(daemon: ScriptedDaemon, predecessor: DaemonSession, successor: string, terminal: string, others: DaemonDesktop[]) {
  daemon.emit({ event: 'session_registered', session: successorOf(predecessor, successor) });
  daemon.emit({
    event: 'profile_arrangement_changed',
    profile: defaultProfile('desktop-s1'),
    desktops: [showing(successor, terminal), ...others],
  });
  daemon.emit({ event: 'session_unregistered', session: predecessor });
  await daemon.idle();
}

async function typeInto(daemon: ScriptedDaemon, paneId: string, key: string) {
  const terminal = document.querySelector(`[data-pane-id="${paneId}"] [aria-label="Terminal input"]`)!;
  fireEvent.keyDown(terminal, { key, code: `Key${key.toUpperCase()}` });
  await daemon.idle();
  return daemon.sentOf('pty_input').slice(-1).map(({ id, data }) => ({ id, data }));
}

describe('App session succession', () => {
  it.each(TERMINALS)('keeps the user, in queue mode, on the pane of %s when it moves on to another session', async (_, terminal) => {
    const s1 = daemonSession('s1', { state: 'waiting_input', ...OWED });
    const s3 = daemonSession('s3', { state: 'waiting_input', ...OWED });
    const { daemon } = await openAttachedTerminals({
      sessions: [s1, s3],
      desktops: [terminalDesktop('s1', terminal), soloDesktop('s3')],
      initialState: { settings: QUEUE, profiles: [defaultProfile('desktop-s1')] },
    });
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(await typeInto(daemon, 'pane-s1', 'a')).toEqual([{ id: terminal, data: 'a' }]);
    const attachments = attachesAndDetaches(daemon).length;

    await succeed(daemon, s1, 's2', terminal, [soloDesktop('s3')]);
    await act(() => vi.advanceTimersByTimeAsync(1000));

    expect(attachesAndDetaches(daemon).slice(attachments)).toEqual([]);
    expect(shownDesktops()).toEqual(['desktop-s1']);
    expect(shownLeaf()).toBe('pane-s1');
    expect(focusedPane()).toBe('pane-s1');
    expect(await typeInto(daemon, 'pane-s1', 'b')).toEqual([{ id: terminal, data: 'b' }]);

    daemon.on('client_hello', () => initialState({
      settings: QUEUE,
      sessions: [successorOf(s1, 's2'), s3],
      desktops: [showing('s2', terminal), soloDesktop('s3')],
      profiles: [defaultProfile('desktop-s1')],
    }));
    const reconnected = await daemon.reconnect();
    await daemon.idle();
    expect(shownLeaf()).toBe('pane-s1');
    expect(reconnected.sent.filter((command) => command.cmd === 'attach_session').map(({ id }) => id)).toContain(terminal);
  });

  it('follows the terminal through successions that happened while the app was away', async () => {
    const s1 = daemonSession('s1', { state: 'waiting_input', ...OWED });
    const s4 = daemonSession('s4', { state: 'waiting_input', ...OWED });
    const { daemon } = await renderApp({
      initialState: {
        sessions: [s1, s4],
        desktops: [terminalDesktop('s1', 'terminal-1'), soloDesktop('s4')],
        profiles: [defaultProfile('desktop-s1')],
      },
    });
    await open(daemon, 's4');
    await open(daemon, 's1');

    daemon.on('client_hello', () => initialState({
      sessions: [successorOf(s1, 's3', { state: 'waiting_input', ...OWED, succeeds: 's2' }), s4],
      desktops: [showing('s3', 'terminal-1'), soloDesktop('s4')],
      profiles: [defaultProfile('desktop-s1')],
    }));
    await daemon.reconnect();
    await daemon.idle();

    expect(shownDesktops()).toEqual(['desktop-s1']);
    expect(shownLeaf()).toBe('pane-s1');
  });

  it.each(TERMINALS)('does not reattach, after a reconnect, %s whose pane and session ended while the app was away', async (_, terminal) => {
    const { daemon } = await openAttachedTerminals({
      sessions: [daemonSession('s3', { state: 'idle' }), daemonSession('s1', { state: 'idle' })],
      desktops: [soloDesktop('s3'), terminalDesktop('s1', terminal)],
    });
    daemon.on('client_hello', () => initialState({ sessions: [daemonSession('s3', { state: 'idle' })], desktops: [soloDesktop('s3')] }));

    const reconnected = await daemon.reconnect();
    await daemon.idle();

    expect(reconnected.sent.filter((command) => command.cmd === 'attach_session').map(({ id }) => id)).toEqual(['s3']);
  });
});
