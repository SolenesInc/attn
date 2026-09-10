import { createRef } from 'react';

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '../test/utils';
import { SettingsModal, type SettingsModalHandle } from './SettingsModal';

const daemonApi = vi.hoisted(() => ({
  sendGetSettings: vi.fn(),
  sendBusStatusGet: vi.fn(() => new Promise(() => {})),
  sendBusSetConsumerEnabled: vi.fn(),
  sendAutoModeGet: vi.fn(() => new Promise(() => {})),
  sendAutoModePromote: vi.fn(),
  sendAutoModeDiscard: vi.fn(),
  sendAutoModePatternAdd: vi.fn(),
  sendAutoModePatternRemove: vi.fn(),
}));
vi.mock('../contexts/DaemonApiContext', () => ({ useDaemonApi: () => daemonApi }));

function renderModal(overrides: Record<string, unknown> = {}) {
  const onSetSetting = vi.fn();
  const onListPlugins = vi.fn().mockResolvedValue({ plugins: [], issues: [] });
  const props = {
    isOpen: true,
    onClose: vi.fn(),
    mutedRepos: [],
    githubHosts: [],
    onUnmuteRepo: vi.fn(),
    mutedAuthors: [],
    onUnmuteAuthor: vi.fn(),
    settings: {},
    endpoints: [],
    plugins: [],
    pluginIssues: [],
    onAddEndpoint: vi.fn().mockResolvedValue({ success: true }),
    onUpdateEndpoint: vi.fn().mockResolvedValue({ success: true }),
    onRemoveEndpoint: vi.fn().mockResolvedValue({ success: true }),
    onSetEndpointRemoteWeb: vi.fn().mockResolvedValue({ success: true }),
    onListPlugins,
    onInstallPlugin: vi.fn().mockResolvedValue({ success: true }),
    onRemovePlugin: vi.fn().mockResolvedValue({ success: true }),
    onSetPluginPriority: vi.fn().mockResolvedValue({ success: true }),
    onSetSetting,
    themePreference: 'system' as const,
    onSetTheme: vi.fn(),
    ...overrides,
  };
  const view = render(<SettingsModal {...props} />);
  const rerender = (next: Record<string, unknown>) =>
    view.rerender(<SettingsModal {...props} {...next} />);
  return { onSetSetting, onListPlugins, rerender };
}

describe('SettingsModal drafts', () => {
  // Every draft seeds itself from a hook effect; one firing on every render instead of on a real
  // change shows up here as a list call that never stops and a test that never finishes.
  it('settles when it opens: one plugin list, nothing written', async () => {
    const { onListPlugins, onSetSetting } = renderModal();

    await waitFor(() => expect(onListPlugins).toHaveBeenCalledTimes(1));

    expect(onListPlugins).toHaveBeenCalledTimes(1);
    expect(onSetSetting).not.toHaveBeenCalled();
  });

  it('keeps a half-typed field when some other setting changes underneath it', async () => {
    const { rerender } = renderModal();
    fireEvent.click(screen.getByTestId('settings-nav-workspace'));

    const input = await screen.findByTestId('settings-projects-directory-input');
    fireEvent.change(input, { target: { value: '/Users/you/half-typed' } });

    rerender({ settings: { default_model_claude: 'sonnet' } });

    expect(await screen.findByTestId('settings-projects-directory-input'))
      .toHaveValue('/Users/you/half-typed');
  });

  it('reseeds a field when its own value changes', async () => {
    const { rerender } = renderModal();
    fireEvent.click(screen.getByTestId('settings-nav-workspace'));

    await screen.findByTestId('settings-projects-directory-input');
    rerender({ settings: { projects_directory: '/Users/you/code' } });

    await waitFor(() => {
      expect(screen.getByTestId('settings-projects-directory-input')).toHaveValue('/Users/you/code');
    });
  });

  it('retains an unfinished draft if its parent hides the modal', async () => {
    const { rerender } = renderModal({ settings: { projects_directory: '/Users/you/code' } });
    fireEvent.click(screen.getByTestId('settings-nav-workspace'));

    const input = await screen.findByTestId('settings-projects-directory-input');
    fireEvent.change(input, { target: { value: '/Users/you/half-typed' } });

    rerender({ isOpen: false });
    rerender({ isOpen: true });

    expect(await screen.findByTestId('settings-projects-directory-input'))
      .toHaveValue('/Users/you/half-typed');
  });

  it('commits a focused field before closing and leaves failed saves open', async () => {
    const save = vi.fn().mockRejectedValueOnce(new Error('Disconnected')).mockResolvedValue(undefined);
    const onClose = vi.fn();
    renderModal({ onSetSetting: save, onClose });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));
    const model = await screen.findByTestId('settings-default-model-claude');
    model.focus();
    fireEvent.change(model, { target: { value: 'sonnet' } });
    await act(async () => { fireEvent.click(screen.getByTestId('settings-close')); });
    expect(onClose).not.toHaveBeenCalled();
    expect(model).toHaveValue('sonnet');
    expect(screen.getByRole('alert')).toHaveTextContent('Disconnected');
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Retry' })); });
    await act(async () => { fireEvent.click(screen.getByTestId('settings-close')); });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(save.mock.calls).toEqual([['default_model_claude', 'sonnet'], ['default_model_claude', 'sonnet']]);
  });

  it('flushes a focused draft through the keyboard shortcut close handle', async () => {
    const ref = createRef<SettingsModalHandle>();
    const onClose = vi.fn();
    const { onSetSetting } = renderModal({ ref, onClose });
    fireEvent.click(screen.getByTestId('settings-nav-backgroundAgents'));
    const field = await screen.findByTestId('settings-chief-model-claude');
    field.focus();
    fireEvent.change(field, { target: { value: 'sonnet' } });
    await act(async () => { await ref.current!.close(); });
    expect(onSetSetting).toHaveBeenCalledWith('chief_model_claude', 'sonnet');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('saves a reversal made before the earlier write is acknowledged', async () => {
    let acknowledge!: () => void;
    const save = vi.fn().mockReturnValueOnce(new Promise<void>((resolve) => { acknowledge = resolve; })).mockResolvedValue(undefined);
    renderModal({ settings: { default_model_claude: 'opus' }, onSetSetting: save });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));
    const input = await screen.findByTestId('settings-default-model-claude');
    fireEvent.change(input, { target: { value: 'sonnet' } });
    fireEvent.blur(input);
    fireEvent.change(input, { target: { value: 'opus' } });
    fireEvent.blur(input);
    await act(async () => { acknowledge(); });
    expect(save.mock.calls).toEqual([['default_model_claude', 'sonnet'], ['default_model_claude', 'opus']]);
    expect(input).toHaveValue('opus');
  });

  it('retains a default-agent reversal while the first selection is saving', async () => {
    let acknowledge!: () => void;
    const save = vi.fn().mockReturnValueOnce(new Promise<void>((resolve) => { acknowledge = resolve; })).mockResolvedValue(undefined);
    const { rerender } = renderModal({ settings: { new_session_agent: 'claude' }, onSetSetting: save });
    fireEvent.click(screen.getByTestId('settings-nav-agents'));
    fireEvent.click(await screen.findByRole('button', { name: 'Codex' }));
    fireEvent.click(screen.getByRole('button', { name: 'Claude' }));
    rerender({ settings: { new_session_agent: 'codex' } });
    expect(screen.getByRole('button', { name: 'Claude' })).toHaveAttribute('aria-checked', 'true');
    await act(async () => { acknowledge(); });
    expect(save.mock.calls).toEqual([['new_session_agent', 'codex'], ['new_session_agent', 'claude']]);
  });

  it('writes an effort override on change, under the model field’s mark', async () => {
    const { onSetSetting } = renderModal();
    fireEvent.click(screen.getByTestId('settings-nav-backgroundAgents'));

    const effort = await screen.findByTestId('settings-chief-effort-claude');
    fireEvent.change(effort, { target: { value: 'high' } });

    expect(onSetSetting).toHaveBeenCalledWith('chief_effort_claude', 'high');
    expect(await screen.findByTestId('settings-chief-model-saved-claude')).toBeInTheDocument();
  });
});
