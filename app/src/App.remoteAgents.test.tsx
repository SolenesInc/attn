import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession, soloDesktop, type DaemonSession } from './test/daemonFixtures';
import { openSession } from './test/appFixtures';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { fakeRects } from './test/layout';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { OutpostReach, OutpostState, type OutpostInfo } from './types/generated';

const host: OutpostInfo = {
  id: 'studio-id', name: 'studio', address: 'studio:2200', state: OutpostState.Connected, reach: OutpostReach.Reachable,
  state_since: '2026-10-27T10:00:00Z',
};
const offline: OutpostInfo = { ...host, state: OutpostState.Lost, reach: OutpostReach.Unreachable };

function setup(outpost = offline, remote: Partial<DaemonSession> = {}, script?: (daemon: ScriptedDaemon) => void) {
  fakeRects((element) => element.tagName === 'CANVAS' ? new DOMRect(0, 0, 800, 600) : null);
  return renderApp({
    script,
    initialState: {
      sessions: [
        daemonSession('remote', { host: host.id, state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-10-27T09:00:00Z', ...remote }),
        daemonSession('local', { state: 'waiting_input', turn_owed: true, turn_opened_at: '2026-10-27T10:00:00Z' }),
      ],
      desktops: [soloDesktop('remote'), soloDesktop('local')],
      outposts: [outpost],
      settings: { outposts_enabled: 'true', queue_mode_enabled: 'true' },
    },
  });
}

describe('Agents on an unreachable outpost', () => {
  it('keeps the remote row, excludes its turn from counts and Cmd+J, and restores it on reconnect', async () => {
    const { daemon } = await setup();
    expect(screen.getByTestId('queue-turn-remote')).toHaveAttribute('data-offline', 'true');
    expect(screen.getByTestId('queue-waiting-card')).toHaveAttribute('data-waiting', '1');
    await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));
    expect(document.querySelector('[data-desktop-id="desktop-local"][data-session-visible="1"]')).not.toBeNull();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [host] }));
    expect(screen.getByTestId('queue-turn-remote')).not.toHaveAttribute('data-offline', 'true');
    expect(screen.getByTestId('queue-waiting-card')).toHaveAttribute('data-waiting', '2');
    await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));
    expect(document.querySelector('[data-desktop-id="desktop-remote"][data-session-visible="1"]')).not.toBeNull();
  });

  it('hides live turn actions and the chief role while rename and priority remain', async () => {
    const { daemon } = await setup(host);
    await openSession(daemon, 'remote');
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [offline] }));
    expect(screen.queryByTestId('queue-settle-remote')).toBeNull();
    expect(screen.queryByTestId('queue-snooze-remote')).toBeNull();
    await gesture(daemon, () => pressShortcut('session.settle'));
    await gesture(daemon, () => pressShortcut('session.snooze'));
    expect(daemon.sentOf('settle_turn')).toEqual([]);
    expect(daemon.sentOf('snooze_turn')).toEqual([]);
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('session-actions-remote')));
    const menu = within(screen.getByRole('menu', { name: 'Actions for remote' }));
    expect(menu.getByRole('menuitem', { name: /Rename session/ })).toBeInTheDocument();
    expect(menu.getByRole('menuitem', { name: /Mark priority/ })).toBeInTheDocument();
    expect(menu.queryByRole('menuitem', { name: /chief/ })).toBeNull();
  });

  it('skips the offline row for next and automatic advance after the local turn closes', async () => {
    const { daemon } = await setup();
    await openSession(daemon, 'local');
    await gesture(daemon, () => pressShortcut('session.next'));
    expect(document.querySelector('[data-desktop-id="desktop-local"][data-session-visible="1"]')).not.toBeNull();
    await gesture(daemon, () => daemon.emit({ event: 'session_state_changed', session: daemonSession('local', { state: 'waiting_input', turn_owed: false }) }));
    expect(document.querySelector('[data-desktop-id="desktop-remote"][data-session-visible="1"]')).toBeNull();
    expect(screen.getByTestId('queue-turn-remote')).toHaveAttribute('data-offline', 'true');
  });

  it('freezes the countdown and disables its cancel shortcut until the outpost is back', async () => {
    const { daemon } = await setup(host, { auto_settle_fires_at: '2999-01-01T00:00:00Z' });
    await openSession(daemon, 'remote');
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [offline] }));
    expect(screen.getByTestId('settling-indicator')).toBeDisabled();
    expect(screen.getByTestId('settling-indicator')).toHaveTextContent('Settling paused');
    expect(document.querySelector('[data-pane-id="pane-remote"] .settling-header-track-fill')).toHaveClass('settling-track-fill--held');
    await gesture(daemon, () => pressShortcut('session.cancelCountdown'));
    expect(daemon.sentOf('cancel_countdown')).toEqual([]);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [host] }));
    expect(screen.getByTestId('settling-indicator')).toBeEnabled();
  });

  it('keeps saved annotations, names the offline host and hides submit until reconnect', async () => {
    const { daemon } = await setup(host, {}, (scripted) => {
      scripted.on('session_messages_get', ({ session_id }) => ({ event: 'session_messages_get_result', session_id, success: true, status: 'ready', messages: [{ key: 'turn-1', markdown: 'Check the parser.' }], truncated: false }));
      scripted.on('session_annotations_get', ({ session_id }) => ({ event: 'session_annotations_get_result', session_id, success: true, generation: 1, annotations: [{ id: 'a1', message_key: 'turn-1', start: 10, end: 16, quote: 'parser', comment: 'why?', emoji: '❓' }] }));
    });
    await openSession(daemon, 'remote');
    const panel = () => within(screen.getByTestId('annotation-panel'));
    expect(panel().getByRole('button', { name: /Send all/ })).toBeInTheDocument();
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [offline] }));
    expect(panel().getByText('parser')).toBeInTheDocument();
    expect(panel().getByRole('status')).toHaveTextContent('studio is unreachable');
    expect(panel().queryByRole('button', { name: /Send all/ })).toBeNull();
    await gesture(daemon, () => pressShortcut('terminal.sendAnnotations'));
    expect(daemon.sentOf('session_annotations_submit')).toEqual([]);
    await gesture(daemon, () => daemon.emit({ event: 'outposts_updated', outposts: [host] }));
    expect(panel().getByRole('button', { name: /Send all/ })).toBeInTheDocument();
  });

  it('hides unsnooze for an offline turn', async () => {
    const { daemon } = await setup(offline, { turn_snoozed_until: '2999-01-01T00:00:00Z' });
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /more agents?/i })));
    expect(screen.queryByTestId('queue-wake-remote')).toBeNull();
    expect(screen.getByTestId('queue-snoozed-remote')).toHaveAttribute('data-offline', 'true');
  });

});
