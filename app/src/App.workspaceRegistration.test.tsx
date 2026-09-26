import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentPane, daemonSession, daemonWorkspace } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

function serveDirectories(daemon: ScriptedDaemon) {
  daemon.on('inspect_path', ({ path }) => ({
    event: 'inspect_path_result',
    success: true,
    inspection: { input_path: path, resolved_path: path, home_path: '/home/me', exists: true, is_directory: true },
  }));
}

async function startSessionIn(daemon: ScriptedDaemon, directory: string) {
  await gesture(daemon, () => pressShortcut('session.newWorkspace'));
  const path = screen.getByTestId('location-picker-path-input');
  fireEvent.change(path, { target: { value: directory } });
  await gesture(daemon, () => fireEvent.keyDown(path, { key: 'Enter' }));
}

describe('App workspace registration', () => {
  it('shows a refused registration at once, even when its rollback is refused too', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { daemon } = await renderApp();
    serveDirectories(daemon);
    daemon.on('register_workspace', () => ({
      event: 'command_error', success: false, cmd: 'register_workspace', error: 'remote endpoint refused the workspace',
    }));
    daemon.on('unregister_workspace', () => ({
      event: 'command_error', success: false, cmd: 'unregister_workspace', error: 'workspace is still in use',
    }));

    await startSessionIn(daemon, '/tmp/repo');

    const [registration] = daemon.sentOf('register_workspace');
    expect(daemon.sentOf('unregister_workspace')).toEqual([{ cmd: 'unregister_workspace', id: registration.id }]);
    expect(within(screen.getByRole('dialog', { name: 'Session was not created' })).getByRole('alert'))
      .toHaveTextContent('remote endpoint refused the workspace');
  });

  it('spawns a new agent in the workspace it registered and leaves attaching it to its pane', async () => {
    const { daemon } = await renderApp();
    serveDirectories(daemon);
    daemon.on('register_workspace', ({ id, title, directory }) => ({
      event: 'workspace_registered',
      workspace: daemonWorkspace(id, { root: null }, { title, directory }),
    }));
    daemon.on('workspace_layout_add_session_pane', ({ workspace_id, pane_id, session_id }) => [
      { event: 'workspace_layout_action_result', action: 'workspace_layout_add_session_pane', workspace_id, pane_id, success: true },
      {
        event: 'workspace_layout_updated',
        workspace_layout: daemonWorkspace(workspace_id, {
          root: { type: 'pane', pane_id },
          panes: [{ ...agentPane(session_id!, workspace_id), pane_id: pane_id! }],
        }).layout!,
      },
    ]);
    daemon.on('spawn_session', ({ id, workspace_id }) => [
      { event: 'spawn_result', id, success: true },
      { event: 'session_registered', session: daemonSession(id, { workspace_id, directory: '/tmp/repo', state: 'working' }) },
    ]);

    await startSessionIn(daemon, '/tmp/repo');

    const [spawn] = daemon.sentOf('spawn_session');
    expect(spawn).toMatchObject({ agent: 'claude', cwd: '/tmp/repo', workspace_id: `workspace-${spawn.id}` });
    expect(daemon.sent.filter((command) =>
      (command.cmd === 'attach_session' || command.cmd === 'pty_resize') && command.id === spawn.id,
    )).toEqual([{ cmd: 'attach_session', id: spawn.id, attach_policy: 'same_app_remount' }]);
  });
});
