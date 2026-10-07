import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { daemonSession, terminalDesktop } from './test/daemonFixtures';

async function renderSessions() {
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      desktops: [terminalDesktop('s1', 'terminal-1'), terminalDesktop('s2', 'terminal-2')],
    },
  });
  await view.daemon.idle();
  return view.daemon;
}

describe('App session exit', () => {
  it('reloads the current agent from the command palette through the existing reload path', async () => {
    const daemon = await renderSessions();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    const input = screen.getByRole('combobox');
    await gesture(daemon, () => fireEvent.change(input, { target: { value: '>Reload this agent' } }));
    await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));

    expect(daemon.sentOf('reload_session')).toEqual([expect.objectContaining({ cmd: 'reload_session', id: 's2' })]);
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('lets the daemon close a cleanly exited session', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'session_exited', id: 'terminal-1', session_id: 's1', exit_code: 0 });
    await daemon.idle();

    expect(daemon.sentOf('unregister')).toEqual([]);
    daemon.emit({ event: 'session_unregistered', session: daemonSession('s1') });
    await daemon.idle();
    expect(screen.queryByRole('button', { name: 'Open s1' })).toBeNull();
  });

  it('keeps the pane of an agent that was killed, so its end stays readable', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'session_exited', id: 'terminal-2', session_id: 's2', exit_code: -1, signal: 'SIGTERM' });
    await daemon.idle();

    expect(daemon.sentOf('unregister')).toEqual([]);
  });

  it('reattaches a respawned terminal with relaunch_restore instead of treating it as an exit', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'runtime_respawned', id: 'terminal-1' });
    await daemon.idle();

    expect(daemon.sentOf('attach_session')).toContainEqual({ cmd: 'attach_session', id: 'terminal-1', attach_policy: 'relaunch_restore' });
    expect(daemon.sentOf('unregister')).toEqual([]);
  });

  it('leaves clean-exit closure to the daemon during and after reload', async () => {
    const daemon = await renderSessions();
    daemon.on('reload_session', () => undefined);
    fireEvent.click(screen.getByRole('button', { name: 'Actions for s1' }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Reload session/ }));
    await daemon.idle();

    daemon.emit({ event: 'session_exited', id: 'terminal-1', session_id: 's1', exit_code: 0 });
    await daemon.idle();
    expect(daemon.sentOf('unregister')).toEqual([]);

    const [reload] = daemon.sentOf('reload_session');
    await gesture(daemon, () => daemon.replyTo(reload, { event: 'reload_session_result', id: 's1', success: true }));
    daemon.emit({ event: 'session_exited', id: 'terminal-1', session_id: 's1', exit_code: 0 });
    await daemon.idle();
    expect(daemon.sentOf('unregister')).toEqual([]);
  });
});
