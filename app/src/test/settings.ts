import { fireEvent, screen } from '@testing-library/react';
import type { EventMessage } from './protocol';
import { renderApp, type AppRender } from './renderApp';
import type { ScriptedDaemon } from './scriptedDaemon';

type InitialState = EventMessage<'initial_state'>;
type Script = (daemon: ScriptedDaemon) => void;

async function renderScriptedSettings(initial: Partial<InitialState>, script: Script): Promise<AppRender> {
  const view = await renderApp({ initialState: initial });
  let settings = initial.settings ?? {};
  view.daemon.on('set_setting', ({ key, value }) => {
    settings = { ...settings, [key]: value };
    return [
      { event: 'settings_updated', success: true, changed_key: key },
      { event: 'settings_updated', request_id: undefined, settings },
    ];
  });
  script(view.daemon);
  return view;
}

const pressSettingsShortcut = () => fireEvent.keyDown(window, { key: ',', metaKey: true });

export async function renderSettings(initial: Partial<InitialState> = {}, script: Script = () => {}): Promise<AppRender> {
  const view = await renderScriptedSettings(initial, script);
  await openSettings(view.daemon);
  return view;
}

export async function openSection(section: string, initial: Partial<InitialState> = {}, script: Script = () => {}): Promise<ScriptedDaemon> {
  const { daemon } = await renderScriptedSettings(initial, script);
  await gesture(daemon, () => {
    pressSettingsShortcut();
    fireEvent.click(screen.getByTestId(`settings-nav-${section}`));
  });
  return daemon;
}

export async function openSettings(daemon: ScriptedDaemon) {
  await gesture(daemon, pressSettingsShortcut);
}

export async function gesture(daemon: ScriptedDaemon, action: () => void) {
  action();
  await daemon.idle();
}

export function savedSettings(daemon: ScriptedDaemon): Array<[string, string]> {
  return daemon.sentOf('set_setting').map(({ key, value }) => [key, value]);
}
