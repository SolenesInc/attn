import { act, fireEvent, render, type RenderResult } from '@testing-library/react';
import App from '../App';
import { defaultShortcut, type ShortcutId } from '../shortcuts/registry';
import { forgetAppMemory } from './appMemory';
import { installScriptedDaemon, type ScriptedDaemon, type ScriptedDaemonOptions } from './scriptedDaemon';

export interface AppRender extends RenderResult {
  daemon: ScriptedDaemon;
}

export async function renderApp(options: ScriptedDaemonOptions = {}): Promise<AppRender> {
  const daemon = installScriptedDaemon(options);
  const result = render(<App />);
  await act(() => daemon.connected());
  return { daemon, ...result };
}

export async function restartApp(running: AppRender, options: ScriptedDaemonOptions = {}): Promise<AppRender> {
  running.unmount();
  forgetAppMemory();
  return renderApp(options);
}

export async function gesture(daemon: ScriptedDaemon, action: () => void) {
  action();
  await daemon.idle();
}

export function pressShortcut(id: ShortcutId, target: Window | Element = window) {
  const { key, code, meta, shift, alt, ctrl } = defaultShortcut(id);
  fireEvent.keyDown(target, { key, code, metaKey: !!meta, shiftKey: !!shift, altKey: !!alt, ctrlKey: !!ctrl });
}
