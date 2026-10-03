import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, renderApp } from './test/renderApp';
import { daemonSession, terminalWorkspace } from './test/daemonFixtures';

async function renderSessions() {
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      workspaces: [terminalWorkspace('s1', 'terminal-1'), terminalWorkspace('s2', 'terminal-2')],
    },
  });
  await view.daemon.idle();
  return view.daemon;
}

describe('App session exit', () => {
  it('closes the session whose terminal exited cleanly', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'session_exited', id: 'terminal-1', session_id: 's1', exit_code: 0 });
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([
      expect.objectContaining({ workspace_id: 'workspace-s1', pane_id: 'pane-s1' }),
    ]);
  });

  it('keeps the pane of an agent that was killed, so its end stays readable', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'session_exited', id: 'terminal-2', session_id: 's2', exit_code: -1, signal: 'SIGTERM' });
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);
  });

  it('reattaches a respawned terminal with relaunch_restore instead of treating it as an exit', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'runtime_respawned', id: 'terminal-1' });
    await daemon.idle();

    expect(daemon.sentOf('attach_session')).toContainEqual({ cmd: 'attach_session', id: 'terminal-1', attach_policy: 'relaunch_restore' });
    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);
  });

  it('keeps the pane of an agent whose reload kills it cleanly, and closes it on a clean exit once the reload is done', async () => {
    const daemon = await renderSessions();
    daemon.on('reload_session', () => undefined);
    fireEvent.click(screen.getByRole('button', { name: 'Actions for s1' }));
    fireEvent.click(screen.getByRole('menuitem', { name: /Reload session/ }));
    await daemon.idle();

    daemon.emit({ event: 'session_exited', id: 'terminal-1', session_id: 's1', exit_code: 0 });
    await daemon.idle();
    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);

    const [reload] = daemon.sentOf('reload_session');
    await gesture(daemon, () => daemon.replyTo(reload, { event: 'reload_session_result', id: 's1', success: true }));
    daemon.emit({ event: 'session_exited', id: 'terminal-1', session_id: 's1', exit_code: 0 });
    await daemon.idle();
    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([
      expect.objectContaining({ workspace_id: 'workspace-s1', pane_id: 'pane-s1' }),
    ]);
  });
});
