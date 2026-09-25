import { describe, expect, it, vi } from 'vitest';
import { renderWithDaemon } from '../test/renderApp';

describe('useDaemonSocket keyed command errors', () => {
  it('matches setting acknowledgements by request id and propagates save failures', async () => {
    const { daemon, api } = await renderWithDaemon();
    const settled: string[] = [];
    const first = api.current.sendSaveSetting('default_model_claude', 'sonnet').then(() => settled.push('first'));
    const second = api.current.sendSaveSetting('default_model_codex', 'test-model')
      .catch((error: Error) => { settled.push(error.message); });
    const [firstRequest, secondRequest] = daemon.sent.filter((command) => command.cmd === 'set_setting');

    daemon.emit({ event: 'settings_updated', changed_key: 'default_model_claude', settings: { default_model_claude: 'sonnet' } });
    daemon.emit({ event: 'settings_updated', request_id: 'another-client', success: true });
    await daemon.idle();
    expect(settled).toEqual([]);

    daemon.emit({ event: 'settings_updated', request_id: secondRequest.request_id, success: false, error: 'Storage unavailable' });
    await second;
    expect(settled).toEqual(['Storage unavailable']);

    daemon.emit({ event: 'settings_updated', request_id: firstRequest.request_id, success: true });
    await first;
    expect(settled).toEqual(['Storage unavailable', 'first']);
  });

  it('rejects a pending session rename with the parked endpoint reason instead of timing out', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const parked = 'endpoint gpu-box is parked: remote binary (abc1234) differs from this client (def5678) — click Sync to update';
    const { daemon, api } = await renderWithDaemon();
    daemon.on('rename_session', () => ({ event: 'command_error', success: false, cmd: 'rename_session', error: parked }));

    await expect(api.current.sendRenameSession('session-1', 'new name')).rejects.toThrow(parked);
  });

  it('rejects a pending workspace rename with the daemon error instead of timing out', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const parked = 'endpoint gpu-box is parked: remote binary differs from this client — click Sync to update';
    const { daemon, api } = await renderWithDaemon();
    daemon.on('rename_workspace', () => ({ event: 'command_error', success: false, cmd: 'rename_workspace', error: parked }));

    await expect(api.current.sendRenameWorkspace('workspace-1', 'new name')).rejects.toThrow(parked);
  });
});
