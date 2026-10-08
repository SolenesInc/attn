import { act, fireEvent, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { openAttachedTerminals, openSession } from './test/appFixtures';
import { daemonSession, terminalDesktop } from './test/daemonFixtures';
import { gesture, restartApp } from './test/renderApp';

const exit = { code: 143, at: '2026-10-07T00:20:04+02:00' };
const ended = () => daemonSession('ended', { state: 'idle', terminal_exit: exit });
const desktop = terminalDesktop('ended', 'dead-terminal');

it('keeps an exited agent stopped across app restart and resumes only on request', async () => {
  const view = await openAttachedTerminals({ sessions: [daemonSession('ended', { state: 'idle' })], desktops: [desktop], output: { 'dead-terminal': 'Final agent output\r\n' } });
  view.daemon.emit({ event: 'session_exited', id: 'dead-terminal', session_id: 'ended', exit_code: 143 });
  view.daemon.emit({ event: 'session_state_changed', session: ended() });
  await view.daemon.idle();
  expect(screen.getByRole('button', { name: 'Resume' })).toBeVisible();
  expect(view.daemon.sentOf('reload_session')).toEqual([]);

  const restarted = await restartApp(view, {
    initialState: { sessions: [ended()], desktops: [desktop] },
    script: (daemon) => {
      daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, running: false, exit, screen: { text: 'Final agent output', cols: 80, rows: 24 } }));
      daemon.on('reload_session', ({ id }) => ({ event: 'reload_session_result', id, success: false, error: 'Directory unavailable' }));
    },
  });
  await openSession(restarted.daemon, 'ended');
  expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('ended')).toContain('Final agent output');
  expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('ended')).not.toContain('Failed to attach');
  expect(restarted.daemon.sentOf('reload_session')).toEqual([]);
  expect(restarted.daemon.sentOf('pty_resize')).toEqual([]);

  const canvas = document.querySelector('canvas')!;
  await gesture(restarted.daemon, () => fireEvent.keyDown(canvas, { key: 'x', code: 'KeyX' }));
  expect(restarted.daemon.sentOf('pty_input')).toEqual([]);

  await gesture(restarted.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Resume' })));
  expect(screen.getByRole('alert')).toHaveTextContent('Directory unavailable');
  expect(window.__TEST_GET_SESSION_PANE_VISIBLE_TEXT?.('ended')).toContain('Final agent output');

  restarted.daemon.on('reload_session', ({ id }) => ({ event: 'reload_session_result', id, success: true }));
  restarted.daemon.on('attach_session', ({ id }) => ({ event: 'attach_result', id, success: true, running: true, cols: 80, rows: 24 }));
  await gesture(restarted.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Resume' })));
  restarted.daemon.emit({ event: 'session_state_changed', session: daemonSession('ended', { state: 'working' }) });
  restarted.daemon.emit({ event: 'runtime_respawned', id: 'dead-terminal' });
  await act(() => restarted.daemon.idle());
  expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
  expect(restarted.daemon.sentOf('attach_session')).toContainEqual(expect.objectContaining({ id: 'dead-terminal', attach_policy: 'relaunch_restore' }));
});

it('closes a stopped agent through the normal close action', async () => {
  const view = await openAttachedTerminals({ sessions: [ended()], desktops: [desktop] });
  await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Close' })));
  expect(view.daemon.sentOf('unregister')).toEqual([{ cmd: 'unregister', id: 'ended' }]);
});
