import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, renderApp } from './test/renderApp';
import { soloDesktop, daemonSession } from './test/daemonFixtures';

async function renderSessions() {
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      desktops: [soloDesktop('s1'), soloDesktop('s2')],
    },
  });
  await view.daemon.idle();
  return view.daemon;
}

describe('App session exit', () => {
  it('closes the pane of an agent that exited cleanly', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'session_exited', id: 's1', exit_code: 0 });
    await daemon.idle();

    expect(daemon.sentOf('unregister')).toEqual([{ cmd: 'unregister', id: 's1' }]);
  });

  it('keeps the pane of an agent that was killed, so its end stays readable', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'session_exited', id: 's2', exit_code: -1, signal: 'SIGTERM' });
    await daemon.idle();

    expect(daemon.sentOf('unregister')).toEqual([]);
  });

  it('reattaches a respawned runtime with relaunch_restore instead of treating it as an exit', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'runtime_respawned', id: 's1' });
    await daemon.idle();

    expect(daemon.sentOf('attach_session')).toContainEqual({ cmd: 'attach_session', id: 's1', attach_policy: 'relaunch_restore' });
    expect(daemon.sentOf('unregister')).toEqual([]);
  });

  it('keeps the pane of an agent whose reload kills it cleanly, and closes it on a clean exit once the reload is done', async () => {
    const daemon = await renderSessions();
    daemon.on('reload_session', () => undefined);
    fireEvent.click(screen.getByRole('button', { name: 'Actions for s1' }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Reload session/ }));
    await daemon.idle();

    daemon.emit({ event: 'session_exited', id: 's1', exit_code: 0 });
    await daemon.idle();
    expect(daemon.sentOf('unregister')).toEqual([]);

    const [reload] = daemon.sentOf('reload_session');
    await gesture(daemon, () => daemon.replyTo(reload, { event: 'reload_session_result', id: 's1', success: true }));
    daemon.emit({ event: 'session_exited', id: 's1', exit_code: 0 });
    await daemon.idle();
    expect(daemon.sentOf('unregister')).toEqual([{ cmd: 'unregister', id: 's1' }]);
  });
});
