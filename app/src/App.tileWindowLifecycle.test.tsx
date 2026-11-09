import { act, fireEvent, screen } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { agentPane, daemonDesktop, daemonSession, defaultProfile, emptyDesktop } from './test/daemonFixtures';
import { pane, split } from './test/desktopLayouts';
import { fakeRects } from './test/layout';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';
import { tileWindowByLabel } from './tileWindow/pool';
import { letTileWindowsOpen } from './test/tileWindows';

const FRAME = { x: 10, y: 20, width: 500, height: 400 };
const PAGE = 'https://example.test/page';
const BROWSER_LABEL_PART = 'tile-browser';

const layout = (popped: boolean) => split('a-split', 'vertical', [pane('s1'), {
  type: 'tile', tile_id: 'tile-browser', tile_kind: 'browser', tile_params: PAGE,
  ...(popped ? { window: FRAME } : {}),
}]);

const browserHostLifecycle = () => vi.mocked(invoke).mock.calls
  .filter(([command]) => command === 'browser_host_mount' || command === 'browser_host_unmount')
  .map(([command]) => command);

const shownTileWindowLabel = () => vi.mocked(invoke).mock.calls
  .filter(([command]) => command === 'tile_window_prepare' || command === 'tile_window_show')
  .map(([, args]) => (args as { label: string }).label)
  .pop();

const lastBrowserUpdate = () => vi.mocked(invoke).mock.calls
  .filter(([command, args]) => command === 'browser_host_update' && String((args as { label: string }).label).includes(BROWSER_LABEL_PART))
  .map(([, args]) => args as { window: string; geometry: { visible: boolean } })
  .pop();

function scriptPopOut(daemon: ScriptedDaemon) {
  const arrange = (popped: boolean) => {
    const desktop = daemon.arrangement.desktop('A')!;
    daemon.arrangement.replace({ ...desktop, tree_json: JSON.stringify(layout(popped)) });
  };
  daemon.on('desktop_pop_out_leaf', (command) => {
    arrange(true);
    return [daemon.arrangement.answer(command), { event: 'profile_action_result', action: command.cmd, success: true }];
  });
  daemon.on('desktop_bring_back_leaf', (command) => {
    arrange(false);
    return [daemon.arrangement.answer(command), { event: 'profile_action_result', action: command.cmd, success: true }];
  });
}

async function openBrowserOnA({ popped = false }: { popped?: boolean } = {}) {
  vi.mocked(isTauri).mockReturnValue(true);
  vi.mocked(invoke).mockImplementation(async (command: string) => (command === 'tile_window_frame_over' ? FRAME : undefined));
  await letTileWindowsOpen();
  fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, 1500, 1000) : null));
  const view = await renderApp({ initialState: {
    sessions: [daemonSession('s1', { state: 'idle' })],
    profiles: [defaultProfile('A')],
    desktops: [
      daemonDesktop('A', { root: layout(popped), panes: [agentPane('s1', 'A')] }, { shortcut_slot: 1 }),
      emptyDesktop('B', { shortcut_slot: 2 }),
      emptyDesktop('C', { shortcut_slot: 3 }),
    ],
  } });
  serveSettings(view.daemon, {});
  scriptPopOut(view.daemon);
  await act(async () => { await openSession(view.daemon, 's1'); });
  return view;
}

describe('a browser tile in its own window', () => {
  it('stays shown while Settings is open in main', async () => {
    const { daemon } = await openBrowserOnA({ popped: true });
    const tileWindowLabel = shownTileWindowLabel();
    expect(lastBrowserUpdate()).toMatchObject({ window: tileWindowLabel, geometry: { visible: true } });

    await gesture(daemon, () => pressShortcut('ui.openSettings'));

    expect(lastBrowserUpdate()).toMatchObject({ window: tileWindowLabel, geometry: { visible: true } });
  });

  it('keeps its live page when brought back to a desktop main has moved away from', async () => {
    const { daemon } = await openBrowserOnA();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('tile-window-toggle-tile-browser')));
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => pressShortcut('desktop.select3'));

    const tileWindowLabel = shownTileWindowLabel();
    await gesture(daemon, () => window.dispatchEvent(new CustomEvent('attn:tile-window-close', { detail: { label: tileWindowLabel } })));
    expect(daemon.sentOf('desktop_bring_back_leaf')).toHaveLength(1);
    await gesture(daemon, () => pressShortcut('desktop.select1'));

    expect(browserHostLifecycle()).toEqual(['browser_host_mount']);
  });

  it('keeps its live page when brought back while main shows the home screen', async () => {
    const { daemon } = await openBrowserOnA();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('tile-window-toggle-tile-browser')));
    await gesture(daemon, () => pressShortcut('session.goToDashboard'));

    const tileWindowLabel = shownTileWindowLabel();
    await gesture(daemon, () => window.dispatchEvent(new CustomEvent('attn:tile-window-close', { detail: { label: tileWindowLabel } })));
    expect(daemon.sentOf('desktop_bring_back_leaf')).toHaveLength(1);
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => pressShortcut('desktop.select3'));

    expect(browserHostLifecycle()).toEqual(['browser_host_mount']);
  });

  it('lets its desktop leave memory once main has shown it after the bring-back', async () => {
    const { daemon } = await openBrowserOnA();
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('tile-window-toggle-tile-browser')));
    await gesture(daemon, () => pressShortcut('session.goToDashboard'));
    const tileWindowLabel = shownTileWindowLabel();
    await gesture(daemon, () => window.dispatchEvent(new CustomEvent('attn:tile-window-close', { detail: { label: tileWindowLabel } })));
    await gesture(daemon, () => pressShortcut('desktop.select1'));

    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => pressShortcut('desktop.select3'));

    expect(browserHostLifecycle()).toEqual(['browser_host_mount', 'browser_host_unmount']);
  });

  it('runs app shortcuts pressed inside it', async () => {
    const { daemon } = await openBrowserOnA({ popped: true });
    const tileWindow = tileWindowByLabel(shownTileWindowLabel()!)!;
    expect(screen.queryByTestId('settings-nav-desktop')).toBeNull();

    await gesture(daemon, () => pressShortcut('ui.openSettings', tileWindow));

    expect(screen.getByTestId('settings-nav-desktop')).toBeTruthy();
  });

  it('brings its tile back once when the pop-out key is pressed inside it', async () => {
    const { daemon } = await openBrowserOnA({ popped: true });
    const tileWindow = tileWindowByLabel(shownTileWindowLabel()!)!;

    await gesture(daemon, () => pressShortcut('tile.popOut', tileWindow));

    expect(daemon.sentOf('desktop_bring_back_leaf')).toHaveLength(1);
    expect(daemon.sentOf('desktop_pop_out_leaf')).toHaveLength(0);
  });
});
