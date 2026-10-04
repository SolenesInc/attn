import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { daemonSession, soloDesktop, type DaemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const queueRows = () => [...screen.getByTestId('sidebar-queue').querySelectorAll('[data-testid^="queue-turn-"]')].map((row) => row.getAttribute('data-session-id'));

function team(): DaemonSession[] {
  return [
    daemonSession('old', { turn_owed: true, turn_opened_at: '2026-01-01T00:00:00Z' }),
    daemonSession('middle', { turn_owed: true, turn_opened_at: '2026-01-02T00:00:00Z' }),
    daemonSession('next', { turn_owed: true, turn_opened_at: '2026-01-03T00:00:00Z' }),
    daemonSession('urgent', { priority: true, turn_owed: true, turn_opened_at: '2026-01-04T00:00:00Z' }),
    daemonSession('busy', { priority: true }),
  ];
}

function launch(sessions = team()) {
  return renderApp({ initialState: {
    settings: { queue_mode_enabled: 'true' }, sessions,
    desktops: sessions.map((session) => soloDesktop(session.id)),
  } });
}

describe('Priority sessions', () => {
  it('orders owed priority turns first on home, the sidebar, the queue bar and jump to waiting', async () => {
    const { daemon } = await launch();
    expect(queueRows()).toEqual(['urgent', 'old', 'middle']);
    expect(within(screen.getByTestId('queue-turn-urgent')).getByRole('img', { name: 'Priority' })).toBeInTheDocument();
    const homeRows = [...document.querySelectorAll('.dashboard .session-row')];
    expect(homeRows.length).toBeGreaterThan(0);
    expect(homeRows[0]).toHaveTextContent('urgent');
    await gesture(daemon, () => pressShortcut('session.jumpToWaiting'));
    expect(screen.getByTestId('queue-turn-urgent')).toHaveClass('selected');
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    expect(screen.getByTestId('queue-bar-pill')).toHaveTextContent(/urgent.*old.*middle/);
    expect(within(screen.getByTestId('queue-bar-pill')).getByRole('img', { name: 'Priority' })).toBeInTheDocument();
  });

  it('advances a settled turn in the middle of the queue to the priority head', async () => {
    const sessions = team();
    const { daemon } = await launch(sessions);
    await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('queue-turn-middle')).getByRole('button', { name: 'Open middle' })));
    await gesture(daemon, () => pressShortcut('session.settle'));
    expect(daemon.sentOf('settle_turn')).toEqual([{ cmd: 'settle_turn', session_id: 'middle' }]);
    daemon.emit({ event: 'sessions_updated', sessions: sessions.map((session) => session.id === 'middle' ? { ...session, turn_owed: false, turn_opened_at: undefined } : session) });
    await daemon.idle();
    expect(screen.getByTestId('queue-turn-urgent')).toHaveClass('selected');
  });

  it('toggles a focused row from the shortcut and the session menu in both directions', async () => {
    const sessions = team();
    const { daemon } = await launch(sessions);
    const row = screen.getByTestId('queue-turn-old');
    const button = within(row).getByRole('button', { name: 'Open old' });
    await gesture(daemon, () => { button.focus(); pressShortcut('session.priority', button); });
    expect(daemon.sentOf('set_session_priority')).toEqual([{ cmd: 'set_session_priority', session_id: 'old', priority: true }]);
    daemon.emit({ event: 'sessions_updated', sessions: sessions.map((session) => session.id === 'old' ? { ...session, priority: true } : session) });
    await daemon.idle();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('session-actions-old')));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Unmark priority' })));
    expect(daemon.sentOf('set_session_priority').slice(-1)[0]).toEqual({ cmd: 'set_session_priority', session_id: 'old', priority: false });
    daemon.emit({ event: 'sessions_updated', sessions });
    await daemon.idle();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('session-actions-old')));
    await gesture(daemon, () => fireEvent.click(screen.getByRole('menuitem', { name: 'Mark priority' })));
    expect(daemon.sentOf('set_session_priority').slice(-1)[0]).toEqual({ cmd: 'set_session_priority', session_id: 'old', priority: true });
  });
  it('toggles the highlighted palette agent without closing the palette', async () => {
    const sessions = team();
    const { daemon } = await launch(sessions);
    await gesture(daemon, () => pressShortcut('ui.actionMenu'));
    const search = screen.getByRole('combobox');
    await gesture(daemon, () => fireEvent.change(search, { target: { value: 'urgent' } }));
    await gesture(daemon, () => pressShortcut('session.priority', search));
    expect(daemon.sentOf('set_session_priority')).toEqual([{ cmd: 'set_session_priority', session_id: 'urgent', priority: false }]);
    daemon.emit({ event: 'sessions_updated', sessions: sessions.map((session) => session.id === 'urgent' ? { ...session, priority: false } : session) });
    await daemon.idle();
    await gesture(daemon, () => pressShortcut('session.priority', search));
    expect(daemon.sentOf('set_session_priority').slice(-1)[0]).toEqual({ cmd: 'set_session_priority', session_id: 'urgent', priority: true });
  });

});
