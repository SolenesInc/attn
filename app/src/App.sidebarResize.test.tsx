import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { pressShortcut, gesture, renderApp } from './test/renderApp';
import { openSection, serveSettings } from './test/settings';

async function renderSidebar(settings: Record<string, string> = {}) {
  const { daemon } = await renderApp({
    initialState: {
      settings,
      sessions: [daemonSession('s1', { state: 'idle' })],
      desktops: [soloDesktop('s1', { shortcut_slot: 1 })],
    },
    script: (scripted) => serveSettings(scripted, settings),
  });
  await daemon.idle();
  return daemon;
}

const handle = () => screen.queryByRole('separator', { name: 'Resize the sidebar' });
const shownWidth = () => document.documentElement.style.getPropertyValue('--sidebar-width');
const widthOf = (px: number) => `min(${px}px, calc(100vw - 480px))`;
const widthSaves = (daemon: Awaited<ReturnType<typeof renderSidebar>>) =>
  daemon.sentOf('set_setting').filter((command) => command.key === 'sidebar_width');

function press(clientX: number) {
  fireEvent.pointerDown(handle()!, { button: 0, pointerId: 1, clientX });
}
const moveTo = (clientX: number) => fireEvent.pointerMove(window, { pointerId: 1, clientX });
const release = (clientX: number) => fireEvent.pointerUp(window, { pointerId: 1, clientX });

describe('Dragging the sidebar', () => {
  it('saves one width, on release', async () => {
    const daemon = await renderSidebar();
    press(240);
    moveTo(260);
    moveTo(300);
    expect(shownWidth()).toBe(widthOf(300));
    expect(widthSaves(daemon)).toEqual([]);
    await gesture(daemon, () => {
      moveTo(320);
      release(320);
    });
    expect(widthSaves(daemon).map((command) => command.value)).toEqual(['320']);
    expect(shownWidth()).toBe(widthOf(320));
  });

  it('stops at each limit and names it', async () => {
    const daemon = await renderSidebar();
    press(240);
    moveTo(-100);
    expect(shownWidth()).toBe(widthOf(200));
    expect(screen.getByText('200px is the narrowest')).toBeInTheDocument();
    moveTo(window.innerWidth);
    expect(shownWidth()).toBe(widthOf(Math.min(600, window.innerWidth - 480)));
    expect(screen.getByText(/is the widest/)).toBeInTheDocument();
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
  });

  it('restores the width and saves nothing on Esc', async () => {
    const daemon = await renderSidebar({ sidebar_width: '300' });
    press(300);
    moveTo(400);
    await gesture(daemon, () => {
      fireEvent.keyDown(window, { key: 'Escape' });
      release(400);
    });
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe(widthOf(300));
  });

  it('restores the width and saves nothing when the window loses focus', async () => {
    const daemon = await renderSidebar({ sidebar_width: '300' });
    press(300);
    moveTo(400);
    expect(shownWidth()).toBe(widthOf(400));
    await gesture(daemon, () => fireEvent.blur(window));
    expect(shownWidth()).toBe(widthOf(300));
    await gesture(daemon, () => release(400));
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe(widthOf(300));
  });

  it('starts a new drag after one was cancelled', async () => {
    const daemon = await renderSidebar({ sidebar_width: '300' });
    press(300);
    moveTo(400);
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    press(300);
    moveTo(340);
    expect(shownWidth()).toBe(widthOf(340));
    await gesture(daemon, () => release(340));
    expect(widthSaves(daemon).map((command) => command.value)).toEqual(['340']);
  });

  it('keeps a stored width the window cannot show when the handle is only clicked', async () => {
    const daemon = await renderSidebar({ sidebar_width: '600' });
    vi.stubGlobal('innerWidth', 800);
    await gesture(daemon, () => {
      press(320);
      release(320);
    });
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe(widthOf(600));
  });

  it('cancels a drag when the sidebar collapses', async () => {
    const daemon = await renderSidebar({ sidebar_width: '300' });
    press(300);
    moveTo(400);
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    expect(handle()).toBeNull();
    await gesture(daemon, () => release(400));
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe(widthOf(300));
  });

  it('clears the setting on a double-click', async () => {
    const daemon = await renderSidebar({ sidebar_width: '300' });
    await gesture(daemon, () => fireEvent.doubleClick(handle()!));
    expect(widthSaves(daemon).map((command) => command.value)).toEqual(['']);
    expect(shownWidth()).toBe(widthOf(240));
  });

  it('follows a settings event, and leaves a live drag alone until it ends', async () => {
    const daemon = await renderSidebar();
    await act(async () => {
      daemon.emit({ event: 'settings_updated', settings: { sidebar_width: '360' } });
      await daemon.idle();
    });
    expect(shownWidth()).toBe(widthOf(360));

    press(360);
    moveTo(380);
    await act(async () => {
      daemon.emit({ event: 'settings_updated', settings: { sidebar_width: '420' } });
      await daemon.idle();
    });
    expect(shownWidth()).toBe(widthOf(380));
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(shownWidth()).toBe(widthOf(420));
  });
});

describe('Stepping the sidebar', () => {
  const savedValues = (daemon: Awaited<ReturnType<typeof renderSidebar>>) => widthSaves(daemon).map((command) => command.value);
  const label = () => document.querySelector<HTMLElement>('.sidebar-resize-label')!;
  const runCommand = async (daemon: Awaited<ReturnType<typeof renderSidebar>>, title: string) => {
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    await gesture(daemon, () => fireEvent.change(screen.getByRole('combobox'), { target: { value: `>${title}` } }));
    await gesture(daemon, () => fireEvent.mouseDown(screen.getByRole('option', { name: new RegExp(title) })));
  };

  it('moves 40px with each key, shows the width, and the label leaves', async () => {
    const daemon = await renderSidebar();
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    onTestFinished(() => { vi.useRealTimers(); });
    expect(label()).not.toBeVisible();

    await gesture(daemon, () => pressShortcut('sidebar.wider'));
    expect(savedValues(daemon)).toEqual(['280']);
    expect(shownWidth()).toBe(widthOf(280));
    expect(label()).toBeVisible();
    expect(label()).toHaveTextContent('280px');

    await gesture(daemon, () => pressShortcut('sidebar.narrower'));
    await gesture(daemon, () => pressShortcut('sidebar.narrower'));
    expect(savedValues(daemon)).toEqual(['280', '240', '200']);
    expect(label()).toHaveTextContent('200px is the narrowest');

    await act(() => vi.advanceTimersByTimeAsync(1500));
    expect(label()).not.toBeVisible();
  });

  it.each([
    ['8 on a Swedish keyboard', { key: '[', code: 'Digit8' }, 8],
    ['9 on a Swedish keyboard', { key: ']', code: 'Digit9' }, 9],
    ['5 on a German keyboard', { key: '[', code: 'Digit5' }, 5],
    ['6 on a German keyboard', { key: ']', code: 'Digit6' }, 6],
  ])('leaves Cmd+Option+%s to the desktop move', async (_, keys, slot) => {
    const { daemon } = await renderApp({
      initialState: {
        sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'idle' })],
        desktops: [soloDesktop('s1', { shortcut_slot: 1 }), soloDesktop('s2', { shortcut_slot: slot })],
      },
      script: (scripted) => serveSettings(scripted),
    });
    await gesture(daemon, () => pressShortcut('desktop.select1'));
    await gesture(daemon, () => fireEvent.keyDown(window, { ...keys, metaKey: true, altKey: true }));
    expect(widthSaves(daemon)).toEqual([]);
    expect(daemon.sentOf('desktop_move_leaf').map((command) => command.target_shortcut_slot)).toEqual([slot]);
  });

  it('steps from the bracket keys whatever they type', async () => {
    const daemon = await renderSidebar();
    await gesture(daemon, () => fireEvent.keyDown(window, { key: '~', code: 'BracketRight', metaKey: true, altKey: true }));
    await gesture(daemon, () => fireEvent.keyDown(window, { key: '˙', code: 'BracketLeft', metaKey: true, altKey: true }));
    expect(savedValues(daemon)).toEqual(['280', '240']);
  });

  it('runs the same steps from the palette, and resets to the default', async () => {
    const daemon = await renderSidebar({ sidebar_width: '300' });
    await runCommand(daemon, 'Widen the sidebar');
    expect(shownWidth()).toBe(widthOf(340));
    await runCommand(daemon, 'Narrow the sidebar');
    expect(shownWidth()).toBe(widthOf(300));
    await runCommand(daemon, 'Reset sidebar width');
    expect(savedValues(daemon)).toEqual(['340', '300', '']);
    expect(shownWidth()).toBe(widthOf(240));
  });

  it.each([
    ['the narrowest', 'sidebar.narrower', '200', 1600, "The sidebar can't go below 200px."],
    ['the widest', 'sidebar.wider', '600', 1600, "The sidebar can't pass 600px."],
    ["the window's share", 'sidebar.wider', '320', 800, "The sidebar can't pass 320px with the window at 800px: tiles keep 480px."],
    ["the window's share from a wider stored width", 'sidebar.wider', '600', 800, "The sidebar can't pass 320px with the window at 800px: tiles keep 480px."],
  ] as const)('names the limit past %s and saves nothing', async (_, id, width, windowPx, message) => {
    const daemon = await renderSidebar({ sidebar_width: width });
    vi.stubGlobal('innerWidth', windowPx);
    onTestFinished(() => { vi.unstubAllGlobals(); });
    await gesture(daemon, () => pressShortcut(id));
    expect(screen.getByText(message)).toBeInTheDocument();
    expect(widthSaves(daemon)).toEqual([]);
  });

  it('shows one message however many times a step hits the limit', async () => {
    const daemon = await renderSidebar({ sidebar_width: '200' });
    for (let press = 0; press < 3; press += 1) await gesture(daemon, () => pressShortcut('sidebar.narrower'));
    expect(screen.getAllByText("The sidebar can't go below 200px.")).toHaveLength(1);
  });

  it('stops exactly on a limit, and steps down from what the window shows', async () => {
    const daemon = await renderSidebar({ sidebar_width: '590' });
    vi.stubGlobal('innerWidth', 1600);
    onTestFinished(() => { vi.unstubAllGlobals(); });
    await gesture(daemon, () => pressShortcut('sidebar.wider'));
    expect(savedValues(daemon)).toEqual(['600']);
    vi.stubGlobal('innerWidth', 800);
    await gesture(daemon, () => pressShortcut('sidebar.narrower'));
    expect(savedValues(daemon)).toEqual(['600', '280']);
  });

  it('says the sidebar is hidden instead of changing a width nobody can see', async () => {
    const daemon = await renderSidebar();
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    await gesture(daemon, () => pressShortcut('sidebar.wider'));
    expect(screen.getByText('The sidebar is hidden. Show it to change its width.')).toBeInTheDocument();
    expect(widthSaves(daemon)).toEqual([]);
  });
});

describe('The sidebar width in Settings', () => {
  const field = () => screen.getByTestId('settings-sidebar-width');

  it('saves a typed width and goes back to the default', async () => {
    const settings = { sidebar_width: '300' };
    const daemon = await openSection('general', { settings });
    expect(field()).toHaveValue(300);

    fireEvent.change(field(), { target: { value: '360' } });
    await gesture(daemon, () => fireEvent.keyDown(field(), { key: 'Enter' }));
    expect(widthSaves(daemon).map((command) => command.value)).toEqual(['360']);
    expect(shownWidth()).toBe(widthOf(360));

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Use default' })));
    expect(widthSaves(daemon).map((command) => command.value)).toEqual(['360', '']);
    expect(shownWidth()).toBe(widthOf(240));
    expect(field()).toHaveValue(null);
    expect(screen.queryByRole('button', { name: 'Use default' })).toBeNull();
  });

  it("shows the daemon's message for a refused width and keeps the stored one", async () => {
    const refusal = 'sidebar_width must be a whole number from 200 to 600, got "900"';
    const daemon = await openSection('general', { settings: { sidebar_width: '300' } }, (scripted) => {
      scripted.on('set_setting', () => ({ event: 'settings_updated', success: false, error: refusal }));
    });
    fireEvent.change(field(), { target: { value: '900' } });
    await gesture(daemon, () => fireEvent.keyDown(field(), { key: 'Enter' }));
    expect(screen.getAllByRole('alert')[0]).toHaveTextContent(refusal);
    expect(shownWidth()).toBe(widthOf(300));

    fireEvent.change(field(), { target: { value: '300' } });
    await gesture(daemon, () => fireEvent.keyDown(field(), { key: 'Enter' }));
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  });

  it('raises the refusal as a notification when a width is refused outside Settings', async () => {
    const refusal = 'sidebar_width must be a whole number from 200 to 600';
    const daemon = await renderSidebar();
    daemon.on('set_setting', () => ({ event: 'settings_updated', success: false, error: refusal }));

    await gesture(daemon, () => pressShortcut('sidebar.wider'));

    expect(widthSaves(daemon)).toHaveLength(1);
    expect(screen.getByRole('alert')).toHaveTextContent(refusal);
  });

  it('follows a width set elsewhere while Settings is open', async () => {
    const daemon = await openSection('general');
    await act(async () => {
      daemon.emit({ event: 'settings_updated', settings: { sidebar_width: '420' } });
      await daemon.idle();
    });
    expect(field()).toHaveValue(420);
  });

  it('offers no way back when the daemon reports the default width', async () => {
    await openSection('general', { settings: { sidebar_width: '240' } });
    expect(field()).toHaveValue(240);
    expect(screen.queryByRole('button', { name: 'Use default' })).toBeNull();
  });
});
