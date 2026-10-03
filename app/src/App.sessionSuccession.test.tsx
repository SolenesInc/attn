import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { openAttachedTerminals } from './test/appFixtures';
import {
  agentPane,
  agentWorkspace,
  daemonSession,
  daemonWorkspace,
  terminalWorkspace,
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

function selectedAgent(): string | null {
  return document.querySelector('.session-item.selected .session-label')?.textContent ?? null;
}

function focusedPane(): string | null {
  return document.activeElement?.closest('[data-pane-id]')?.getAttribute('data-pane-id') ?? null;
}

function shownWorkspaces() {
  return Array.from(document.querySelectorAll('.session-terminal-workspace[data-session-visible="1"]'))
    .map((workspace) => workspace.getAttribute('data-workspace-id'));
}

function attachesAndDetaches(daemon: ScriptedDaemon) {
  return daemon.sent.filter((command) => command.cmd === 'attach_session' || command.cmd === 'detach_session');
}

async function open(daemon: ScriptedDaemon, id: string) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: `Open ${id}` })));
}

function showing(predecessor: DaemonSession, successor: string, terminal: string) {
  const workspaceId = predecessor.workspace_id;
  return daemonWorkspace(workspaceId, {
    root: { type: 'pane', pane_id: `pane-${predecessor.id}` },
    panes: [{ ...agentPane(predecessor.id, workspaceId, terminal), session_id: successor }],
  });
}

function successorOf(predecessor: DaemonSession, successor: string) {
  return daemonSession(successor, { workspace_id: predecessor.workspace_id, state: 'idle', succeeds: predecessor.id });
}

// In the daemon's order: the successor, then the pane showing it, then the predecessor's end.
async function succeed(daemon: ScriptedDaemon, predecessor: DaemonSession, successor: string, terminal: string) {
  daemon.emit({ event: 'session_registered', session: successorOf(predecessor, successor) });
  daemon.emit({ event: 'workspace_layout_updated', workspace_layout: showing(predecessor, successor, terminal).layout! });
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
  it.each(TERMINALS)('keeps the user, in queue mode, on %s when it moves on to another session', async (_, terminal) => {
    const s1 = daemonSession('s1', { state: 'waiting_input', ...OWED });
    const s3 = daemonSession('s3', { state: 'waiting_input', ...OWED });
    const { daemon } = await openAttachedTerminals({
      sessions: [s3, s1],
      workspaces: [agentWorkspace('s3'), terminalWorkspace('s1', terminal)],
      initialState: { settings: QUEUE },
    });
    await open(daemon, 's1');
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(await typeInto(daemon, 'pane-s1', 'a')).toEqual([{ id: terminal, data: 'a' }]);
    const attachments = attachesAndDetaches(daemon).length;

    await succeed(daemon, s1, 's2', terminal);
    await act(() => vi.advanceTimersByTimeAsync(1000));

    expect(attachesAndDetaches(daemon).slice(attachments)).toEqual([]);
    expect(selectedAgent()).toBe('s2');
    expect(shownWorkspaces()).toEqual(['workspace-s1']);
    expect(focusedPane()).toBe('pane-s1');
    expect(await typeInto(daemon, 'pane-s1', 'b')).toEqual([{ id: terminal, data: 'b' }]);

    daemon.on('client_hello', () => initialState({
      settings: QUEUE,
      sessions: [s3, successorOf(s1, 's2')],
      workspaces: [agentWorkspace('s3'), showing(s1, 's2', terminal)],
    }));
    const reconnected = await daemon.reconnect();
    await daemon.idle();
    expect(reconnected.sent.filter((command) => command.cmd === 'attach_session').map(({ id }) => id).sort())
      .toEqual(['s3', terminal].sort());
  });

  it('keeps the agent shown through every step of its terminal moving on', async () => {
    const s1 = daemonSession('s1');
    const { daemon } = await openAttachedTerminals({
      sessions: [s1, daemonSession('s3')],
      workspaces: [terminalWorkspace('s1', 'terminal-1'), agentWorkspace('s3')],
    });
    const attachments = attachesAndDetaches(daemon).length;
    const shown = () => ({ selected: selectedAgent(), workspaces: shownWorkspaces() });

    daemon.emit({ event: 'session_registered', session: successorOf(s1, 's2') });
    expect(shown()).toEqual({ selected: 's1', workspaces: ['workspace-s1'] });
    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: showing(s1, 's2', 'terminal-1').layout! });
    expect(shown()).toEqual({ selected: 's2', workspaces: ['workspace-s1'] });
    daemon.emit({ event: 'session_unregistered', session: s1 });
    expect(shown()).toEqual({ selected: 's2', workspaces: ['workspace-s1'] });
    await daemon.idle();

    expect(shown()).toEqual({ selected: 's2', workspaces: ['workspace-s1'] });
    expect(attachesAndDetaches(daemon).slice(attachments)).toEqual([]);
  });

  it.each([
    ['names the session its removed predecessor succeeded', 's0'],
    ['names no session, its removed predecessor having none', undefined],
  ])('keeps the user, in queue mode, on a terminal when the session it moves on to %s', async (_, succeeds) => {
    const s1 = daemonSession('s1', { state: 'waiting_input', ...OWED });
    const s3 = daemonSession('s3', { state: 'waiting_input', ...OWED });
    const { daemon } = await openAttachedTerminals({
      sessions: [s3, s1],
      workspaces: [agentWorkspace('s3'), terminalWorkspace('s1', 'terminal-1')],
      initialState: { settings: QUEUE },
    });
    await open(daemon, 's1');
    await act(() => vi.advanceTimersByTimeAsync(1000));
    const attachments = attachesAndDetaches(daemon).length;

    daemon.emit({ event: 'session_registered', session: daemonSession('s2', { workspace_id: s1.workspace_id, state: 'idle', succeeds }) });
    daemon.emit({ event: 'workspace_layout_updated', workspace_layout: showing(s1, 's2', 'terminal-1').layout! });
    daemon.emit({ event: 'session_unregistered', session: s1 });
    await daemon.idle();
    await act(() => vi.advanceTimersByTimeAsync(1000));

    expect(selectedAgent()).toBe('s2');
    expect(shownWorkspaces()).toEqual(['workspace-s1']);
    expect(focusedPane()).toBe('pane-s1');
    expect(attachesAndDetaches(daemon).slice(attachments)).toEqual([]);
  });

  it.each([
    ['', {}],
    [' in queue mode', QUEUE],
  ])('follows the terminal through successions that happened while the app was away%s', async (_, settings) => {
    const s1 = daemonSession('s1', { state: 'waiting_input', ...OWED });
    const s4 = daemonSession('s4', { state: 'waiting_input', ...OWED });
    const { daemon } = await renderApp({
      initialState: { settings, sessions: [s4, s1], workspaces: [agentWorkspace('s4'), terminalWorkspace('s1', 'terminal-1')] },
    });
    await open(daemon, 's4');
    await open(daemon, 's1');

    const s3 = daemonSession('s3', { workspace_id: s1.workspace_id, state: 'waiting_input', ...OWED, succeeds: 's2' });
    daemon.on('client_hello', () => initialState({
      settings,
      sessions: [s4, s3],
      workspaces: [agentWorkspace('s4'), showing(s1, 's3', 'terminal-1')],
    }));
    await daemon.reconnect();
    await daemon.idle();

    expect(selectedAgent()).toBe('s3');
    expect(shownWorkspaces()).toEqual(['workspace-s1']);
  });

  it('puts the successor where its predecessor was in the agent history', async () => {
    const s1 = daemonSession('s1');
    const { daemon } = await renderApp({
      initialState: { sessions: [s1, daemonSession('s3')], workspaces: [terminalWorkspace('s1', 'terminal-1'), agentWorkspace('s3')] },
    });
    await open(daemon, 's1');
    await open(daemon, 's3');

    await succeed(daemon, s1, 's2', 'terminal-1');
    fireEvent.keyDown(window, { key: '[', metaKey: true });
    await daemon.idle();

    expect(selectedAgent()).toBe('s2');
  });

  it('falls back to the successor of the agent the user was on before when the open one closes', async () => {
    const s1 = daemonSession('s1');
    const s3 = daemonSession('s3');
    const { daemon } = await renderApp({
      initialState: {
        sessions: [s1, s3, daemonSession('s4')],
        workspaces: [terminalWorkspace('s1', 'terminal-1'), agentWorkspace('s3'), agentWorkspace('s4')],
      },
    });
    await open(daemon, 's1');
    await open(daemon, 's3');

    await succeed(daemon, s1, 's2', 'terminal-1');
    daemon.emit({ event: 'session_unregistered', session: s3 });
    await daemon.idle();

    expect(selectedAgent()).toBe('s2');
  });

  it('leaves the user on a reopened predecessor while its successor still names it', async () => {
    const s1 = daemonSession('s1');
    const { daemon } = await renderApp({
      initialState: { sessions: [s1, daemonSession('s3')], workspaces: [terminalWorkspace('s1', 'terminal-1'), agentWorkspace('s3')] },
    });
    await open(daemon, 's1');
    await succeed(daemon, s1, 's2', 'terminal-1');

    const reopened = daemonSession('s1', { workspace_id: 'ws-reopened' });
    daemon.emit({ event: 'session_registered', session: reopened });
    daemon.emit({
      event: 'workspace_state_changed',
      workspace: daemonWorkspace('ws-reopened', {
        root: { type: 'pane', pane_id: 'pane-reopened' },
        panes: [{ ...agentPane('s1', 'ws-reopened', 'terminal-9'), pane_id: 'pane-reopened' }],
      }),
    });
    await open(daemon, 's1');
    daemon.emit({ event: 'sessions_updated', sessions: [
      reopened,
      successorOf(s1, 's2'),
      daemonSession('s3'),
    ] });
    await daemon.idle();

    expect(selectedAgent()).toBe('s1');
  });

  it('leaves the user on a reopened predecessor when its closed successor is reopened too', async () => {
    const s1 = daemonSession('s1');
    const { daemon } = await renderApp({
      initialState: { sessions: [s1, daemonSession('s3')], workspaces: [terminalWorkspace('s1', 'terminal-1'), agentWorkspace('s3')] },
    });
    await open(daemon, 's1');
    await succeed(daemon, s1, 's2', 'terminal-1');
    daemon.emit({ event: 'session_unregistered', session: successorOf(s1, 's2') });

    const reopened = daemonSession('s1', { workspace_id: 'ws-reopened' });
    daemon.emit({ event: 'session_registered', session: reopened });
    daemon.emit({
      event: 'workspace_state_changed',
      workspace: daemonWorkspace('ws-reopened', {
        root: { type: 'pane', pane_id: 'pane-reopened' },
        panes: [{ ...agentPane('s1', 'ws-reopened', 'terminal-9'), pane_id: 'pane-reopened' }],
      }),
    });
    await open(daemon, 's1');
    daemon.emit({ event: 'session_registered', session: successorOf(s1, 's2') });
    await daemon.idle();

    expect(selectedAgent()).toBe('s1');
  });

  it.each(TERMINALS)('does not reattach, after a reconnect, %s whose pane and session ended while the app was away', async (_, terminal) => {
    const { daemon } = await openAttachedTerminals({
      sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s3', { state: 'idle' })],
      workspaces: [terminalWorkspace('s1', terminal), agentWorkspace('s3')],
    });
    await open(daemon, 's3');
    daemon.on('client_hello', () => initialState({ sessions: [daemonSession('s3', { state: 'idle' })], workspaces: [agentWorkspace('s3')] }));

    const reconnected = await daemon.reconnect();
    await daemon.idle();

    expect(reconnected.sent.filter((command) => command.cmd === 'attach_session').map(({ id }) => id)).toEqual(['s3']);
  });
});
