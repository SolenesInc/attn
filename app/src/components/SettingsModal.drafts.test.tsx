import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, renderApp } from '../test/renderApp';
import type { CommandMessage } from '../test/protocol';
import type { ScriptedDaemon } from '../test/scriptedDaemon';
import { openSection, openSettings, savedSettings } from '../test/settings';

function holdSaves(daemon: ScriptedDaemon) {
  const held: CommandMessage<'set_setting'>[] = [];
  daemon.on('set_setting', (command) => {
    held.push(command);
  });
  return {
    acknowledge: async (settings: Record<string, string>) => {
      const command = held.shift()!;
      daemon.emit({ event: 'settings_updated', request_id: command.request_id, success: true, changed_key: command.key });
      daemon.emit({ event: 'settings_updated', settings });
      await daemon.idle();
    },
  };
}

const settingsModal = () => screen.queryByTestId('settings-modal');

describe('SettingsModal drafts', () => {
  it('settles when it opens: one read of each settings source, nothing written', async () => {
    const { daemon } = await renderApp();
    daemon.on('list_plugins', () => ({ event: 'plugins_updated', plugins: [], issues: [] }));
    await daemon.idle();
    const before = daemon.sent.length;

    await openSettings(daemon);

    expect(daemon.sent.slice(before).map(({ cmd }) => cmd).sort()).toEqual([
      'automode_get',
      'delegation_preferences_get',
      'list_plugins',
    ]);
  });

  it('keeps a half-typed field when some other setting changes underneath it', async () => {
    const daemon = await openSection('workspace');
    fireEvent.change(screen.getByTestId('settings-projects-directory-input'), { target: { value: '/Users/you/half-typed' } });

    daemon.emit({ event: 'settings_updated', settings: { default_model_claude: 'sonnet' } });
    await daemon.idle();

    expect(screen.getByTestId('settings-projects-directory-input')).toHaveValue('/Users/you/half-typed');
  });

  it('reseeds a field when its own value changes', async () => {
    const daemon = await openSection('workspace');

    daemon.emit({ event: 'settings_updated', settings: { projects_directory: '/Users/you/code' } });
    await daemon.idle();

    expect(screen.getByTestId('settings-projects-directory-input')).toHaveValue('/Users/you/code');
  });

  it('saves a typed draft when Escape closes settings and shows it on reopen', async () => {
    const daemon = await openSection('workspace', { settings: { projects_directory: '/Users/you/code' } });
    fireEvent.change(screen.getByTestId('settings-projects-directory-input'), { target: { value: '/Users/you/half-typed' } });

    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(settingsModal()).toBeNull();
    expect(savedSettings(daemon)).toEqual([['projects_directory', '/Users/you/half-typed']]);
    await openSettings(daemon);

    expect(screen.getByTestId('settings-projects-directory-input')).toHaveValue('/Users/you/half-typed');
  });

  it('commits a focused field before closing and leaves failed saves open', async () => {
    let refusals = 1;
    const daemon = await openSection('agents', {}, (scripted) => {
      scripted.on('set_setting', ({ key, value }) => (refusals-- > 0
        ? { event: 'settings_updated', success: false, error: 'database is locked' }
        : [
          { event: 'settings_updated', success: true, changed_key: key },
          { event: 'settings_updated', request_id: undefined, settings: { [key]: value } },
        ]));
    });
    const model = screen.getByTestId('settings-default-model-claude');
    model.focus();
    fireEvent.change(model, { target: { value: 'sonnet' } });

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-close')));
    expect(settingsModal()).not.toBeNull();
    expect(model).toHaveValue('sonnet');
    expect(within(settingsModal()!).getByRole('alert')).toHaveTextContent('database is locked');

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })));
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('settings-close')));
    expect(settingsModal()).toBeNull();
    expect(savedSettings(daemon)).toEqual([['default_model_claude', 'sonnet'], ['default_model_claude', 'sonnet']]);
  });

  it('flushes a focused draft when the settings shortcut closes it', async () => {
    const daemon = await openSection('backgroundAgents');
    const field = screen.getByTestId('settings-chief-model-claude');
    field.focus();
    fireEvent.change(field, { target: { value: 'sonnet' } });

    await openSettings(daemon);

    expect(savedSettings(daemon)).toEqual([['chief_model_claude', 'sonnet']]);
    expect(settingsModal()).toBeNull();
  });

  it('saves a reversal made before the earlier write is acknowledged', async () => {
    let saves!: ReturnType<typeof holdSaves>;
    const daemon = await openSection('agents', { settings: { default_model_claude: 'opus' } }, (scripted) => {
      saves = holdSaves(scripted);
    });
    const input = screen.getByTestId('settings-default-model-claude');
    fireEvent.change(input, { target: { value: 'sonnet' } });
    fireEvent.blur(input);
    fireEvent.change(input, { target: { value: 'opus' } });
    fireEvent.blur(input);
    await daemon.idle();

    await saves.acknowledge({ default_model_claude: 'sonnet' });
    await saves.acknowledge({ default_model_claude: 'opus' });

    expect(savedSettings(daemon)).toEqual([['default_model_claude', 'sonnet'], ['default_model_claude', 'opus']]);
    expect(input).toHaveValue('opus');
  });

  it('retains a default-agent reversal while the first selection is saving', async () => {
    let saves!: ReturnType<typeof holdSaves>;
    const daemon = await openSection('agents', { settings: { new_session_agent: 'claude' } }, (scripted) => {
      saves = holdSaves(scripted);
    });
    fireEvent.click(screen.getByRole('button', { name: 'Codex' }));
    fireEvent.click(screen.getByRole('button', { name: 'Claude' }));
    await daemon.idle();

    daemon.emit({ event: 'settings_updated', changed_key: 'new_session_agent', settings: { new_session_agent: 'codex' } });
    await daemon.idle();
    expect(screen.getByRole('button', { name: 'Claude' })).toHaveAttribute('aria-pressed', 'true');

    await saves.acknowledge({ new_session_agent: 'codex' });
    await saves.acknowledge({ new_session_agent: 'claude' });
    expect(savedSettings(daemon)).toEqual([['new_session_agent', 'codex'], ['new_session_agent', 'claude']]);
    expect(screen.getByRole('button', { name: 'Claude' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('writes an effort override on change, under the model field’s mark', async () => {
    const daemon = await openSection('backgroundAgents');

    await gesture(daemon, () => fireEvent.change(screen.getByTestId('settings-chief-effort-claude'), { target: { value: 'high' } }));

    expect(savedSettings(daemon)).toEqual([['chief_effort_claude', 'high']]);
    expect(screen.getByTestId('settings-chief-model-saved-claude')).toBeInTheDocument();
  });
});
