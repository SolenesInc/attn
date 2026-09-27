import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import type { CommandMessage } from './test/protocol';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { openSection, savedSettings } from './test/settings';

function holdSaves(daemon: ScriptedDaemon) {
  const held: CommandMessage<'set_setting'>[] = [];
  daemon.on('set_setting', (command) => {
    held.push(command);
  });
  return async (settings: Record<string, string>) => {
    const command = held.shift()!;
    daemon.emit({ event: 'settings_updated', request_id: command.request_id, success: true, changed_key: command.key });
    daemon.emit({ event: 'settings_updated', settings });
    await daemon.idle();
  };
}

async function openAgentsHoldingSaves() {
  let acknowledge!: ReturnType<typeof holdSaves>;
  const daemon = await openSection('agents', {}, (scripted) => {
    acknowledge = holdSaves(scripted);
  });
  return { daemon, acknowledge, model: screen.getByTestId('settings-default-model-claude') };
}

describe('App settings autosave', () => {
  it('says Saving until the daemon acknowledges the write, and Saved only after', async () => {
    const { daemon, acknowledge, model } = await openAgentsHoldingSaves();
    fireEvent.change(model, { target: { value: 'sonnet' } });

    await gesture(daemon, () => fireEvent.blur(model));
    expect(screen.getByText('Saving…')).toBeInTheDocument();
    expect(screen.queryByText('Saved')).toBeNull();

    await acknowledge({ default_model_claude: 'sonnet' });
    expect(screen.queryByText('Saving…')).toBeNull();
    expect(screen.getByText('Saved')).toBeInTheDocument();
  });

  it('keeps what is typed while a save is out and writes it only when the field is committed', async () => {
    const { daemon, acknowledge, model } = await openAgentsHoldingSaves();
    fireEvent.change(model, { target: { value: 'sonnet' } });
    await gesture(daemon, () => fireEvent.blur(model));
    fireEvent.change(model, { target: { value: 'still typing' } });

    await acknowledge({ default_model_claude: 'sonnet' });
    expect(model).toHaveValue('still typing');
    expect(savedSettings(daemon)).toEqual([['default_model_claude', 'sonnet']]);

    await gesture(daemon, () => fireEvent.blur(model));
    expect(savedSettings(daemon)).toEqual([['default_model_claude', 'sonnet'], ['default_model_claude', 'still typing']]);
  });
});
