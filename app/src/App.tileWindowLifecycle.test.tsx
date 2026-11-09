import { act, fireEvent, screen } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import { openSession } from './test/appFixtures';
import { agentPane, daemonDesktop, daemonSession, defaultProfile, emptyDesktop } from './test/daemonFixtures';
import { pane, split } from './test/desktopLayouts';
import { fakeRects } from './test/layout';
import { press, serveLaunches, serveMachine } from './test/locations';
import { defaultShortcut } from './shortcuts/registry';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';
import { letTileWindowsOpen } from './test/tileWindows';
import { browserHostLabel } from './browser/host';

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

async function openBrowserOnA({ popped = false, settings = {} }: { popped?: boolean; settings?: Record<string, string> } = {}) {
  vi.mocked(isTauri).mockReturnValue(true);
  vi.mocked(invoke).mockImplementation(async (command: string) => (command === 'tile_window_frame_over' ? FRAME : undefined));
  const tileWindows = await letTileWindowsOpen();
  fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, 1500, 1000) : null));
  const view = await renderApp({ initialState: {
    sessions: [daemonSession('s1', { state: 'idle' })],
    profiles: [defaultProfile('A')],
    settings,
    desktops: [
      daemonDesktop('A', { root: layout(popped), panes: [agentPane('s1', 'A')] }, { shortcut_slot: 1 }),
      emptyDesktop('B', { shortcut_slot: 2 }),
      emptyDesktop('C', { shortcut_slot: 3 }),
    ],
  } });
  serveSettings(view.daemon, settings);
  scriptPopOut(view.daemon);
  await act(async () => { await openSession(view.daemon, 's1'); });
  return { ...view, tileWindows };
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
    const { daemon, tileWindows } = await openBrowserOnA({ popped: true });
    const tileWindow = tileWindows.get(shownTileWindowLabel()!)!;
    expect(screen.queryByTestId('settings-nav-desktop')).toBeNull();

    await gesture(daemon, () => pressShortcut('ui.openSettings', tileWindow));

    expect(vi.mocked(invoke).mock.calls.some(([command]) => command === 'tile_window_raise_main')).toBe(true);
    expect(screen.getByTestId('settings-nav-desktop')).toBeTruthy();
  });

  it('brings its tile back once when the pop-out key is pressed inside it', async () => {
    const { daemon, tileWindows } = await openBrowserOnA({ popped: true });
    const tileWindow = tileWindows.get(shownTileWindowLabel()!)!;

    await gesture(daemon, () => pressShortcut('tile.popOut', tileWindow));

    expect(daemon.sentOf('desktop_bring_back_leaf')).toHaveLength(1);
    expect(daemon.sentOf('desktop_pop_out_leaf')).toHaveLength(0);
  });

  it('brings its tile back with Command-W instead of closing it', async () => {
    const { daemon, tileWindows } = await openBrowserOnA({ popped: true });
    const tileWindow = tileWindows.get(shownTileWindowLabel()!)!;
    await gesture(daemon, () => fireEvent.keyDown(tileWindow, { key: 'w', metaKey: true }));
    expect(daemon.sentOf('desktop_bring_back_leaf')).toEqual([expect.objectContaining({ desktop_id: 'A', leaf_id: 'tile-browser' })]);
    expect(daemon.sentOf('desktop_remove_leaf')).toEqual([]);
  });

  it('routes a native browser shortcut to the popped browser after main switches desktops', async () => {
    const { daemon } = await openBrowserOnA({ popped: true });
    await gesture(daemon, () => pressShortcut('desktop.select2'));
    await gesture(daemon, () => window.dispatchEvent(new CustomEvent('attn:native-browser-shortcut', {
      detail: { label: browserHostLabel('A', 'tile-browser'), id: 'tile.popOut' },
    })));
    expect(daemon.sentOf('desktop_bring_back_leaf')).toEqual([expect.objectContaining({ desktop_id: 'A', leaf_id: 'tile-browser' })]);
    expect(daemon.sentOf('desktop_remove_leaf')).toEqual([]);
  });

  it('pops out the focused native browser on main', async () => {
    const { daemon } = await openBrowserOnA();
    await gesture(daemon, () => window.dispatchEvent(new CustomEvent('attn:native-browser-shortcut', {
      detail: { label: browserHostLabel('A', 'tile-browser'), id: 'tile.popOut' },
    })));
    expect(daemon.sentOf('desktop_pop_out_leaf')).toEqual([expect.objectContaining({ desktop_id: 'A', leaf_id: 'tile-browser' })]);
  });

  it('runs another action from a shared native chord in main', async () => {
    const { daemon } = await openBrowserOnA({ popped: true });
    await gesture(daemon, () => window.dispatchEvent(new CustomEvent('attn:native-browser-shortcut', {
      detail: { label: browserHostLabel('A', 'tile-browser'), id: 'ui.openSettings' },
    })));
    expect(vi.mocked(invoke).mock.calls.some(([command]) => command === 'tile_window_raise_main')).toBe(true);
    expect(screen.getByTestId('settings-nav-desktop')).toBeTruthy();
  });

  it('runs a row action from a native shared chord in the main window', async () => {
    const { daemon } = await openBrowserOnA({ settings: { narrowest_tile_cols: '66' } });
    await gesture(daemon, () => window.dispatchEvent(new CustomEvent('attn:native-browser-shortcut', {
      detail: { label: browserHostLabel('A', 'tile-browser'), id: 'shelf.fewerTiles' },
    })));
    expect(daemon.sentOf('set_setting')).toEqual([expect.objectContaining({ key: 'narrowest_tile_cols' })]);
  });
});

describe('a terminal in its own window', () => {
  async function openPoppedTerminal(settings: Record<string, string> = {}, firstSession: Parameters<typeof daemonSession>[1] = {}, secondSession: Parameters<typeof daemonSession>[1] = {}, twoPopped = false, mainGrid = false) {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockImplementation(async (command: string) => (command === 'tile_window_frame_over' ? FRAME : undefined));
    const tileWindows = await letTileWindowsOpen();
    fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, 1500, 1000) : null));
    const { daemon } = await renderApp({ initialState: {
      sessions: [daemonSession('s1', { state: 'idle', ...firstSession }), daemonSession('s2', { state: 'idle', ...secondSession }), ...(twoPopped ? [daemonSession('s3', { state: 'idle' })] : []), ...(mainGrid ? ['s3', 's4', 's5', 's6'].map((id) => daemonSession(id, { state: 'idle' })) : [])],
      profiles: [defaultProfile('B')],
      settings,
      desktops: [
        daemonDesktop('A', {
          root: twoPopped ? split('a-split', 'vertical', [{ ...pane('s1'), window: FRAME }, { ...pane('s3'), window: FRAME }]) : { ...pane('s1'), window: FRAME },
          panes: [agentPane('s1', 'A'), ...(twoPopped ? [agentPane('s3', 'A')] : [])],
        }, { shortcut_slot: 1 }),
        daemonDesktop('B', {
          root: mainGrid ? split('b-left', 'vertical', [pane('s3'), split('b-right', 'vertical', [split('b-top', 'horizontal', [pane('s4'), split('b-bottom', 'horizontal', [pane('s2'), pane('s5')])]), pane('s6')])]) : pane('s2'),
          panes: ['s2', ...(mainGrid ? ['s3', 's4', 's5', 's6'] : [])].map((id) => agentPane(id, 'B')),
        }, { shortcut_slot: 2 }),
      ],
    } });
    serveSettings(daemon, settings);
    serveLaunches(daemon);
    await act(async () => { await openSession(daemon, 's2'); });
    return { daemon, tileWindows, tileWindow: tileWindows.get(shownTileWindowLabel()!)! };
  }

  it('splits the popped terminal on its own desktop after main switches', async () => {
    const { daemon, tileWindow } = await openPoppedTerminal();
    await gesture(daemon, () => pressShortcut('terminal.splitVertical', tileWindow));
    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({
      cwd: '/tmp/s1', spawned_from: 's1',
      placement: expect.objectContaining({ desktop_id: 'A', anchor_pane_id: 'pane-s1' }),
    })]);
  });

  it('splits the terminal in the window that received the key when two are popped', async () => {
    const { daemon, tileWindows } = await openPoppedTerminal({}, {}, {}, true);
    for (const id of ['s1', 's3']) {
      const targetWindow = [...tileWindows.values()].find((candidate) => candidate.document.querySelector(`[data-pane-id="pane-${id}"]`));
      expect(targetWindow).toBeTruthy();
      await gesture(daemon, () => pressShortcut('terminal.splitVertical', targetWindow!));
    }
    expect(daemon.sentOf('spawn_session')).toEqual(['s1', 's3'].map((id) => expect.objectContaining({
      spawned_from: id, cwd: `/tmp/${id}`,
      placement: expect.objectContaining({ desktop_id: 'A', anchor_pane_id: `pane-${id}` }),
    })));
  });

  it('keeps tile shortcuts available while main shows Settings', async () => {
    const { daemon, tileWindow } = await openPoppedTerminal();
    await gesture(daemon, () => pressShortcut('ui.openSettings'));
    await gesture(daemon, () => pressShortcut('terminal.splitVertical', tileWindow));
    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({
      spawned_from: 's1',
      placement: expect.objectContaining({ desktop_id: 'A', anchor_pane_id: 'pane-s1' }),
    })]);
  });

  it('keeps tile shortcuts available while main shows the snooze menu', async () => {
    const { daemon, tileWindow } = await openPoppedTerminal({ queue_mode_enabled: 'true' }, {}, { turn_owed: true });
    await gesture(daemon, () => pressShortcut('session.snooze'));
    expect(screen.getByRole('menu', { name: /^Snooze/ })).toBeTruthy();
    await gesture(daemon, () => pressShortcut('terminal.splitVertical', tileWindow));
    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({
      spawned_from: 's1',
      placement: expect.objectContaining({ desktop_id: 'A', anchor_pane_id: 'pane-s1' }),
    })]);
  });

  it('runs session shortcuts on the main desktop', async () => {
    const { daemon, tileWindow } = await openPoppedTerminal();
    await gesture(daemon, () => pressShortcut('session.priority', tileWindow));
    expect(vi.mocked(invoke).mock.calls.some(([command]) => command === 'tile_window_raise_main')).toBe(true);
    expect(daemon.sentOf('set_session_priority')).toEqual([expect.objectContaining({ session_id: 's2', priority: true })]);
  });

  it('leaves the desktop unchanged for row and directional shortcuts in a popped tile', async () => {
    const { daemon, tileWindow } = await openPoppedTerminal({ narrowest_tile_cols: '30' }, {}, {}, false, true);
    const target = tileWindow.document.querySelector('.session-terminal-desktop')!;
    const mainSurface = document.querySelector('[data-session-terminal-desktop="B"]')!;
    const switches = daemon.sentOf('desktop_set_current').length;
    const focusMoves = daemon.sentOf('desktop_show_leaf').length;
    expect(mainSurface.getAttribute('data-zoomed-pane-id')).toBe('');
    expect(mainSurface.getAttribute('data-maximized-pane-id')).toBe('');
    expect(mainSurface.getAttribute('data-active-pane-id')).toBe('pane-s2');
    for (const id of ['s3', 's4', 's5', 's6']) {
      expect(mainSurface.querySelector(`[data-pane-id="pane-${id}"]`)).toBeTruthy();
    }
    for (const id of [
      'shelf.carryLeft', 'shelf.carryRight', 'shelf.sendToShelf',
      'shelf.fewerTiles', 'shelf.moreTiles', 'terminal.toggleZoom', 'terminal.toggleMaximize',
      'terminal.focusLeft', 'terminal.focusRight', 'terminal.focusUp', 'terminal.focusDown',
    ] as const) {
      const { key, code, meta, shift, alt, ctrl } = defaultShortcut(id);
      await gesture(daemon, () => {
        expect(fireEvent.keyDown(target, { key, code, metaKey: !!meta, shiftKey: !!shift, altKey: !!alt, ctrlKey: !!ctrl })).toBe(false);
      });
      expect(mainSurface.getAttribute('data-zoomed-pane-id')).toBe('');
      expect(mainSurface.getAttribute('data-maximized-pane-id')).toBe('');
      expect(mainSurface.getAttribute('data-active-pane-id')).toBe('pane-s2');
    }
    expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
    expect(daemon.sentOf('set_setting')).toEqual([]);
    expect(daemon.sentOf('desktop_bring_back_leaf')).toEqual([]);
    expect(daemon.sentOf('desktop_set_current')).toHaveLength(switches);
    expect(daemon.sentOf('desktop_show_leaf')).toHaveLength(focusMoves);
    await gesture(daemon, () => pressShortcut('terminal.focusRight'));
    expect(daemon.sentOf('desktop_show_leaf')).toHaveLength(focusMoves + 1);
  });

  it.each(['true', 'false'])('keeps the popped tile as split origin through the location picker (launcher %s)', async (launcher) => {
    const { daemon, tileWindow } = await openPoppedTerminal({ new_session_launcher_enabled: launcher }, { directory: '' });
    serveMachine(daemon, { recent: ['/tmp/picked'] });
    await gesture(daemon, () => pressShortcut('terminal.splitVertical', tileWindow));
    expect(vi.mocked(invoke).mock.calls.some(([command]) => command === 'tile_window_raise_main')).toBe(true);
    await press(daemon, 'Enter');
    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({
      cwd: '/tmp/picked', spawned_from: 's1',
      placement: expect.objectContaining({ desktop_id: 'A', anchor_pane_id: 'pane-s1' }),
    })]);
  });

  it('uses the rebound pop-out shortcut to bring the tile back', async () => {
    const settings = { keybindings_config: JSON.stringify({ version: 1, overrides: { 'tile.popOut': { key: 'y', meta: true, shift: true } } }) };
    const { daemon, tileWindow } = await openPoppedTerminal(settings);
    await gesture(daemon, () => fireEvent.keyDown(tileWindow, { key: 'y', metaKey: true, shiftKey: true }));
    expect(daemon.sentOf('desktop_bring_back_leaf')).toEqual([expect.objectContaining({ desktop_id: 'A', leaf_id: 'pane-s1' })]);
  });

  it('uses a chord to bring the originating tile back', async () => {
    const settings = { keybindings_config: JSON.stringify({ version: 1, overrides: { 'tile.popOut': { leader: { key: 'y', meta: true, shift: true }, then: { key: 'j' } } } }) };
    const { daemon, tileWindow } = await openPoppedTerminal(settings);
    expect(vi.mocked(invoke).mock.calls.filter(([command]) => command === 'browser_shortcut_configure').pop()?.[1]).toMatchObject({
      config: {
        defaultMenu: false,
        popOut: { leader: { key: 'y', meta: true, shift: true }, then: { key: 'j' } },
        chords: expect.arrayContaining([expect.objectContaining({ id: 'tile.popOut' })]),
      },
    });
    await gesture(daemon, () => fireEvent.keyDown(tileWindow, { key: 'y', metaKey: true, shiftKey: true }));
    expect(daemon.sentOf('desktop_bring_back_leaf')).toHaveLength(0);
    await gesture(daemon, () => fireEvent.keyDown(tileWindow, { key: 'j' }));
    expect(daemon.sentOf('desktop_bring_back_leaf')).toEqual([expect.objectContaining({ desktop_id: 'A', leaf_id: 'pane-s1' })]);
  });

  it('updates the native browser binding when settings change', async () => {
    const { daemon } = await openPoppedTerminal();
    const nativeConfig = () => vi.mocked(invoke).mock.calls.filter(([command]) => command === 'browser_shortcut_configure').pop()?.[1];
    expect(nativeConfig()).toMatchObject({ config: { defaultMenu: true, popOut: { key: 'o', meta: true, shift: true } } });
    const chord = { leader: { key: 'y', meta: true, shift: true }, then: { key: 'j' } };
    await gesture(daemon, () => daemon.emit({
      event: 'settings_updated', settings: { keybindings_config: JSON.stringify({ version: 1, overrides: { 'tile.popOut': chord } }) },
    }));
    expect(nativeConfig()).toMatchObject({ config: { defaultMenu: false, popOut: chord } });
    await gesture(daemon, () => daemon.emit({
      event: 'settings_updated', settings: { keybindings_config: JSON.stringify({ version: 1, overrides: { 'tile.popOut': null } }) },
    }));
    expect(nativeConfig()).toMatchObject({ config: { defaultMenu: false, popOut: null } });
  });

  it('opens find in the popped terminal rather than the main terminal', async () => {
    const { daemon, tileWindow } = await openPoppedTerminal();
    await gesture(daemon, () => pressShortcut('terminal.find', tileWindow));
    expect(tileWindow.document.querySelector('[data-testid="ghostty-find-input"]')).toBeTruthy();
    expect(document.querySelector('[data-testid="ghostty-find-input"]')).toBeNull();
  });

  it('targets the new tile after a window is reused', async () => {
    const { daemon, tileWindow } = await openPoppedTerminal();
    await gesture(daemon, () => daemon.arrange((desktops) => desktops.map((desktop) => {
      if (desktop.id === 'A') return { ...desktop, tree_json: JSON.stringify(pane('s1')), revision: desktop.revision + 1 };
      if (desktop.id === 'B') return { ...desktop, tree_json: JSON.stringify({ ...pane('s2'), window: FRAME }), revision: desktop.revision + 1 };
      return desktop;
    })));
    await gesture(daemon, () => pressShortcut('terminal.splitVertical', tileWindow));
    expect(daemon.sentOf('spawn_session')).toEqual([expect.objectContaining({
      cwd: '/tmp/s2', spawned_from: 's2',
      placement: expect.objectContaining({ desktop_id: 'B', anchor_pane_id: 'pane-s2' }),
    })]);
  });

  it('sends annotations from the popped terminal', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockImplementation(async (command: string) => (command === 'tile_window_frame_over' ? FRAME : undefined));
    const tileWindows = await letTileWindowsOpen();
    fakeRects((element) => (element.classList.contains('session-terminal-panes') ? new DOMRect(0, 0, 1500, 1000) : null));
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })],
        profiles: [defaultProfile('B')],
        desktops: [
          daemonDesktop('A', { root: { ...pane('s1'), window: FRAME }, panes: [agentPane('s1', 'A')] }),
          daemonDesktop('B', { root: pane('s2'), panes: [agentPane('s2', 'B')] }),
        ],
      },
      script: (scripted) => {
        scripted.on('session_annotations_get', ({ session_id }) => ({
          event: 'session_annotations_get_result', session_id, success: true, generation: 1,
          annotations: [{ id: 'one', message_key: 'turn-1', start: 0, end: 5, quote: 'hello', emoji: '❓', comment: 'why?' }],
        }));
        scripted.on('session_messages_get', ({ session_id }) => ({
          event: 'session_messages_get_result', session_id, success: true, status: 'ready',
          messages: [{ key: 'turn-1', markdown: 'hello world' }], truncated: false,
        }));
        scripted.on('session_annotations_submit', () => undefined);
      },
    });
    await act(async () => { await openSession(daemon, 's2'); });
    const tileWindow = tileWindows.get(shownTileWindowLabel()!)!;
    await gesture(daemon, () => pressShortcut('terminal.sendAnnotations', tileWindow));
    expect(daemon.sentOf('session_annotations_submit')).toEqual([expect.objectContaining({ session_id: 's1', text: expect.stringContaining('hello') })]);
  });
});
