import { describe, expect, it } from 'vitest';
import { renderApp } from './test/renderApp';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';

async function renderSessions() {
  const view = await renderApp({
    initialState: {
      sessions: [daemonSession('s1'), daemonSession('s2')],
      workspaces: [agentWorkspace('s1'), agentWorkspace('s2')],
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

    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([
      expect.objectContaining({ workspace_id: 'workspace-s1', pane_id: 'pane-s1' }),
    ]);
  });

  it('keeps the pane of an agent that was killed, so its end stays readable', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'session_exited', id: 's2', exit_code: -1, signal: 'SIGTERM' });
    await daemon.idle();

    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);
  });

  it('reattaches a respawned runtime with relaunch_restore instead of treating it as an exit', async () => {
    const daemon = await renderSessions();

    daemon.emit({ event: 'runtime_respawned', id: 's1' });
    await daemon.idle();

    expect(daemon.sentOf('attach_session')).toContainEqual({ cmd: 'attach_session', id: 's1', attach_policy: 'relaunch_restore' });
    expect(daemon.sentOf('workspace_layout_close_pane')).toEqual([]);
  });
});
