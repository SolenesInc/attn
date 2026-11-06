import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { pressShortcut, gesture, renderApp } from './test/renderApp';
import { serveSettings } from './test/settings';

const SHELF_ON = { shelf_layout_enabled: 'true' };

async function renderSidebar(settings: Record<string, string>) {
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
    const daemon = await renderSidebar(SHELF_ON);
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
    const daemon = await renderSidebar(SHELF_ON);
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
    const daemon = await renderSidebar({ ...SHELF_ON, sidebar_width: '300' });
    press(300);
    moveTo(400);
    await gesture(daemon, () => {
      fireEvent.keyDown(window, { key: 'Escape' });
      release(400);
    });
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe(widthOf(300));
  });

  it('keeps a stored width the window cannot show when the handle is only clicked', async () => {
    const daemon = await renderSidebar({ ...SHELF_ON, sidebar_width: '600' });
    vi.stubGlobal('innerWidth', 800);
    await gesture(daemon, () => {
      press(320);
      release(320);
    });
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe(widthOf(600));
  });

  it('cancels a drag when the sidebar collapses', async () => {
    const daemon = await renderSidebar({ ...SHELF_ON, sidebar_width: '300' });
    press(300);
    moveTo(400);
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    expect(handle()).toBeNull();
    await gesture(daemon, () => release(400));
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe(widthOf(300));
  });

  it('clears the setting on a double-click', async () => {
    const daemon = await renderSidebar({ ...SHELF_ON, sidebar_width: '300' });
    await gesture(daemon, () => fireEvent.doubleClick(handle()!));
    expect(widthSaves(daemon).map((command) => command.value)).toEqual(['']);
    expect(shownWidth()).toBe(widthOf(240));
  });

  it('follows a settings event, and leaves a live drag alone until it ends', async () => {
    const daemon = await renderSidebar(SHELF_ON);
    await act(async () => {
      daemon.emit({ event: 'settings_updated', settings: { ...SHELF_ON, sidebar_width: '360' } });
      await daemon.idle();
    });
    expect(shownWidth()).toBe(widthOf(360));

    press(360);
    moveTo(380);
    await act(async () => {
      daemon.emit({ event: 'settings_updated', settings: { ...SHELF_ON, sidebar_width: '420' } });
      await daemon.idle();
    });
    expect(shownWidth()).toBe(widthOf(380));
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(shownWidth()).toBe(widthOf(420));
  });

  it('drops the width when the shelf turns off during a drag', async () => {
    const daemon = await renderSidebar({ ...SHELF_ON, sidebar_width: '300' });
    press(300);
    moveTo(400);
    await act(async () => {
      daemon.emit({ event: 'settings_updated', settings: { sidebar_width: '300' } });
      await daemon.idle();
    });
    expect(handle()).toBeNull();
    expect(shownWidth()).toBe('');
    await gesture(daemon, () => release(400));
    expect(widthSaves(daemon)).toEqual([]);
    expect(shownWidth()).toBe('');
  });

  it('has no handle and no width with the shelf off', async () => {
    await renderSidebar({ sidebar_width: '300' });
    expect(handle()).toBeNull();
    expect(shownWidth()).toBe('');
  });
});
