import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { fakeRects } from './test/layout';
import { gesture, pressShortcut, renderApp } from './test/renderApp';

const SETTINGS = { asks_enabled: 'true' };
const RUN = daemonSession('run', { automation: { run_id: 'run', definition_id: 1, definition_name: 'Sweep', trigger_type: 'scheduled' } });

async function renderChrome(settings: Record<string, string>) {
  const { daemon } = await renderApp({
    initialState: { settings, sessions: [daemonSession('s1')], desktops: [soloDesktop('s1')] },
  });
  await daemon.idle();
  return daemon;
}

function groupLabels(testId: string) {
  return within(screen.getByTestId(testId)).getAllByRole('button').map((button) => button.getAttribute('aria-label') ?? button.title);
}

const OTHER_BUTTONS = [
  'Show the garden',
  expect.stringMatching(/^Open Notebook/),
  expect.stringMatching(/^Open Ledger/),
  'Show Automations',
  expect.stringMatching(/^Asks/),
  expect.stringMatching(/^Switch profile/),
];

describe('the title bar', () => {
  it('holds one toolbar in desktop flow', async () => {
    const daemon = await renderChrome(SETTINGS);
    expect(groupLabels('title-bar-left')).toEqual([
      'New agent', 'Commands', 'Switch to queue flow', 'Collapse sidebar', 'Show Notifications',
    ]);
    expect(groupLabels('title-bar-right')).toEqual(OTHER_BUTTONS);
    expect(document.querySelector('.sidebar:not(.collapsed)')).not.toBeNull();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' })));
    const rail = document.querySelector('.icon-rail') as HTMLElement;
    expect(within(rail).getByRole('button', { name: 'Home' })).toBeVisible();
    expect(within(rail).queryByRole('button', { name: 'Commands' })).toBeNull();
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show sidebar' })));
    expect(document.querySelector('.sidebar:not(.collapsed)')).not.toBeNull();
  });

  it('holds the same groups in queue flow and keeps the queue bar inside the band when collapsed', async () => {
    const daemon = await renderChrome({ ...SETTINGS, queue_mode_enabled: 'true' });
    expect(groupLabels('title-bar-left')).toEqual([
      'New agent', 'Commands', 'Switch to desktop flow', 'Collapse sidebar', 'Show Notifications',
    ]);
    expect(groupLabels('title-bar-right')).toEqual(OTHER_BUTTONS);
    expect(screen.queryByTestId('queue-bar')).toBeNull();

    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    const band = screen.getByTestId('title-bar');
    expect(within(band).getByTestId('queue-bar-pill')).toBeVisible();
    expect(within(band).getByTestId('queue-bar-desktops')).toBeVisible();
    expect(screen.getAllByRole('button', { name: 'Commands' })).toHaveLength(1);
    expect(groupLabels('title-bar-left')[3]).toBe('Show sidebar');
    expect(document.querySelector('.sidebar')).toBeNull();
  });

  it('shrinks the queue bar to the current desktop and a +N chip when the chips overflow the room', async () => {
    const CHIP_STEP = 28;
    const BAR_WIDTH = 100;
    fakeRects((element) => {
      if (element.dataset.testid === 'queue-bar') return new DOMRect(0, 0, BAR_WIDTH, 32);
      if (element.dataset.testid === 'queue-bar-desktops') {
        return new DOMRect(40, 0, element.children.length * CHIP_STEP, 32);
      }
      return null;
    });
    const ids = ['s1', 's2', 's3', 's4'];
    const { daemon } = await renderApp({
      initialState: {
        settings: { ...SETTINGS, queue_mode_enabled: 'true' },
        sessions: [...ids.map((id) => daemonSession(id)), RUN],
        desktops: [...ids.map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })), soloDesktop('run')],
      },
    });
    await daemon.idle();
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));

    await gesture(daemon, () => pressShortcut('desktop.select2'));
    expect(screen.getByTestId('queue-bar-desktop-2')).toHaveClass('is-current');
    for (const slot of [1, 3, 4]) expect(screen.queryByTestId(`queue-bar-desktop-${slot}`)).toBeNull();
    expect(screen.queryByTestId('queue-bar-runs')).toBeNull();
    expect(screen.getByTestId('queue-bar-desktop-extras').textContent).toMatch(/^\+[3-9]$/);

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('queue-bar-desktop-extras')));
    expect(document.querySelector('.desktop-overview-scrim')).not.toBeNull();
  });

  it('brings the desktop chips back when the next desktop leaves room for them, at the same bar width', async () => {
    let chipStep = 28;
    fakeRects((element) => {
      if (element.dataset.testid === 'queue-bar') return new DOMRect(0, 0, 100, 32);
      if (element.dataset.testid === 'queue-bar-desktops') {
        return new DOMRect(40, 0, element.children.length * chipStep, 32);
      }
      return null;
    });
    const ids = ['s1', 's2', 's3', 's4'];
    const { daemon } = await renderApp({
      initialState: {
        settings: { ...SETTINGS, queue_mode_enabled: 'true' },
        sessions: ids.map((id) => daemonSession(id)),
        desktops: ids.map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })),
      },
    });
    await daemon.idle();
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    expect(screen.getByTestId('queue-bar-desktop-extras')).toBeVisible();

    chipStep = 10;
    await gesture(daemon, () => pressShortcut('desktop.select3'));
    for (const slot of [1, 2, 3, 4]) expect(screen.getByTestId(`queue-bar-desktop-${slot}`)).toBeVisible();
  });

  it('opens the profile list under the selector at the right end of the band', async () => {
    const daemon = await renderChrome(SETTINGS);
    const selector = within(screen.getByTestId('title-bar-right')).getByTitle(/^Switch profile/);
    await gesture(daemon, () => fireEvent.click(selector));
    expect(screen.getByRole('menu', { name: 'Switch profile' })).toBeVisible();
  });

  it('keeps every desktop chip when they fit', async () => {
    fakeRects((element) => {
      if (element.dataset.testid === 'queue-bar') return new DOMRect(0, 0, 900, 32);
      if (element.dataset.testid === 'queue-bar-desktops') return new DOMRect(40, 0, 200, 32);
      return null;
    });
    const ids = ['s1', 's2', 's3', 's4'];
    const { daemon } = await renderApp({
      initialState: {
        settings: { ...SETTINGS, queue_mode_enabled: 'true' },
        sessions: [...ids.map((id) => daemonSession(id)), RUN],
        desktops: [...ids.map((id, index) => soloDesktop(id, { shortcut_slot: index + 1 })), soloDesktop('run')],
      },
    });
    await daemon.idle();
    await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
    for (const slot of [1, 2, 3, 4]) expect(screen.getByTestId(`queue-bar-desktop-${slot}`)).toBeVisible();
    expect(screen.getByTestId('queue-bar-runs')).toBeVisible();
  });

  it('opens the palette from its toolbar button', async () => {
    const daemon = await renderChrome(SETTINGS);
    await gesture(daemon, () => fireEvent.click(within(screen.getByTestId('title-bar-left')).getByRole('button', { name: 'Commands' })));
    await act(() => vi.advanceTimersToNextFrame());
    expect(screen.getByRole('combobox', { name: 'Commands' })).toHaveFocus();
  });
});

describe('a surface that holds the window', () => {
  it('leaves the toolbar in place but dead until it closes', async () => {
    const daemon = await renderChrome(SETTINGS);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /^Open Notebook/ })));
    expect(screen.getByTestId('title-bar')).toHaveClass('title-bar--covered');
    await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));
    expect(screen.getByTestId('title-bar')).not.toHaveClass('title-bar--covered');
  });
});
