import { describe, expect, it, vi } from 'vitest';
import { renderWithDaemon } from '../test/renderApp';

describe('useDaemonSocket workspace registration errors', () => {
  it('rejects a pending registration with the daemon error instead of timing out', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { daemon, api } = await renderWithDaemon();
    daemon.on('register_workspace', () => ({
      event: 'command_error',
      success: false,
      cmd: 'register_workspace',
      error: 'remote endpoint refused the workspace',
    }));

    await expect(api.current.sendRegisterWorkspace('workspace-1', 'Workspace', '/tmp/repo'))
      .rejects.toThrow('remote endpoint refused the workspace');
  });

  it('rejects a pending unregistration with the daemon error instead of timing out', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { daemon, api } = await renderWithDaemon();
    daemon.on('unregister_workspace', () => ({
      event: 'command_error',
      success: false,
      cmd: 'unregister_workspace',
      error: 'workspace is still in use',
    }));

    await expect(api.current.sendUnregisterWorkspace('workspace-1')).rejects.toThrow('workspace is still in use');
  });
});
