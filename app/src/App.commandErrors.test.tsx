import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import type { CommandMessage } from './test/protocol';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { openSection } from './test/settings';

const parked = 'endpoint gpu-box is parked: remote binary (abc1234) differs from this client (def5678) — click Sync to update';

async function rename(daemon: ScriptedDaemon, dialog: string, value: string) {
  const input = within(screen.getByRole('dialog', { name: dialog })).getByRole('textbox');
  fireEvent.change(input, { target: { value } });
  await gesture(daemon, () => fireEvent.keyDown(input, { key: 'Enter' }));
  return screen.getByRole('dialog', { name: dialog });
}

function renderWorkspace() {
  return renderApp({
    initialState: {
      sessions: [daemonSession('s1', { label: 'working' })],
      workspaces: [agentWorkspace('s1')],
    },
  });
}

describe('App command errors', () => {
  it('settles each setting save by its own request id and shows the one that failed', async () => {
    const held: CommandMessage<'set_setting'>[] = [];
    const daemon = await openSection('agents', {}, (scripted) => {
      scripted.on('set_setting', (command) => { held.push(command); });
    });
    for (const [agent, model] of [['claude', 'sonnet'], ['codex', 'test-model']]) {
      const field = screen.getByTestId(`settings-default-model-${agent}`);
      fireEvent.change(field, { target: { value: model } });
      fireEvent.blur(field);
    }
    await daemon.idle();
    const [first, second] = held;
    const settings = within(screen.getByTestId('settings-modal'));

    daemon.emit({ event: 'settings_updated', changed_key: 'default_model_claude', settings: { default_model_claude: 'sonnet' } });
    daemon.emit({ event: 'settings_updated', request_id: 'another-client', success: true });
    await daemon.idle();
    expect(settings.getByText('Saving…')).toBeInTheDocument();
    expect(settings.queryByRole('alert')).toBeNull();

    daemon.emit({ event: 'settings_updated', request_id: second.request_id, success: false, error: 'Storage unavailable' });
    await daemon.idle();
    expect(settings.getByRole('alert')).toHaveTextContent('Storage unavailable');
    expect(settings.getByText('Saving…')).toBeInTheDocument();

    daemon.emit({ event: 'settings_updated', request_id: first.request_id, success: true });
    await daemon.idle();
    expect(settings.queryByText('Saving…')).toBeNull();
    expect(settings.getAllByRole('alert')).toHaveLength(1);
  });

  it('shows the parked endpoint reason when a session rename is refused', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { daemon } = await renderWorkspace();
    daemon.on('rename_session', () => ({ event: 'command_error', success: false, cmd: 'rename_session', error: parked }));

    fireEvent.click(screen.getByRole('button', { name: 'Actions for working' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Rename session' }));
    const dialog = await rename(daemon, 'Rename session', 'new name');

    expect(daemon.sentOf('rename_session')).toEqual([{ cmd: 'rename_session', session_id: 's1', label: 'new name' }]);
    expect(dialog).toHaveTextContent(parked);
  });

  it('shows the daemon error when a workspace rename is refused', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const { daemon } = await renderWorkspace();
    daemon.on('rename_workspace', () => ({ event: 'command_error', success: false, cmd: 'rename_workspace', error: parked }));

    fireEvent.click(screen.getByRole('button', { name: 'Rename workspace s1' }));
    const dialog = await rename(daemon, 'Rename workspace', 'new name');

    expect(daemon.sentOf('rename_workspace')).toEqual([
      { cmd: 'rename_workspace', workspace_id: 'workspace-s1', title: 'new name' },
    ]);
    expect(dialog).toHaveTextContent(parked);
  });

  it('finishes a repeated PR refresh on the daemon reply and leaves no stale timeout behind', async () => {
    const { daemon } = await renderApp();

    await gesture(daemon, () => pressShortcut('session.refreshPRs'));
    await gesture(daemon, () => pressShortcut('session.refreshPRs'));
    expect(daemon.sentOf('refresh_prs')).toHaveLength(2);
    expect(screen.getByRole('button', { name: /^Refresh PRs/ })).toBeDisabled();

    daemon.emit({ event: 'refresh_prs_result', success: true });
    await daemon.idle();
    expect(screen.getByRole('button', { name: /^Refresh PRs/ })).toBeEnabled();

    await act(() => vi.advanceTimersByTimeAsync(10 * 60_000));
    expect(screen.getByRole('button', { name: /^Refresh PRs/ })).toBeEnabled();
  });
});
