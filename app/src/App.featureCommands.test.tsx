import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import { savedSettings } from './test/settings';
import type { ScriptedDaemon } from './test/scriptedDaemon';

async function search(daemon: ScriptedDaemon, query: string) {
  await gesture(daemon, () => pressShortcut('ui.actionMenu'));
  fireEvent.change(screen.getByRole('combobox'), { target: { value: query } });
  await daemon.idle();
  return within(screen.getByRole('dialog', { name: 'Commands' })).queryAllByRole('option');
}

describe('Feature commands', () => {
  it('turns an off feature on from the palette', async () => {
    const { daemon } = await renderApp();
    const [option] = await search(daemon, '>launcher 2.0');
    expect(option?.textContent).toContain('Turn on Launcher 2.0');
    await gesture(daemon, () => fireEvent.mouseDown(option!));
    expect(savedSettings(daemon)).toContainEqual(['new_session_launcher_enabled', 'true']);
  });

  it('turns an on feature off from the palette', async () => {
    const { daemon } = await renderApp({ initialState: { settings: { new_session_launcher_enabled: 'true' } } });
    const [option] = await search(daemon, '>launcher 2.0');
    expect(option?.textContent).toContain('Turn off Launcher 2.0');
    await gesture(daemon, () => fireEvent.mouseDown(option!));
    expect(savedSettings(daemon)).toContainEqual(['new_session_launcher_enabled', 'false']);
  });

  it('stays out of the list until a search asks for it', async () => {
    const { daemon } = await renderApp();
    const options = await search(daemon, '>');
    expect(options.map((option) => option.textContent).join()).not.toContain('Launcher 2.0');
  });
});
