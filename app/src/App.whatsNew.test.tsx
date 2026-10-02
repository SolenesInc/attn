import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, onTestFinished } from 'vitest';
import { WHATS_NEW_BANNER_STORAGE_KEY, WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from './hooks/useWhatsNew';
import { openAttachedTerminals } from './test/appFixtures';
import { daemonSession, soloDesktop } from './test/daemonFixtures';
import { stubNavigatorPlatform } from './test/platformStub';
import { gesture, pressShortcut, renderApp, restartApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const whatsNew = () => screen.queryByRole('dialog', { name: "What's new" });
const introBanner = () => screen.queryByTestId('intro-banner');
const stepTitle = () => within(whatsNew()!).getByRole('heading', { level: 2 }).textContent;
const stepKeys = () =>
  within(within(whatsNew()!).getByRole('list')).getAllByRole('listitem').map((row) => row.textContent);

async function launchAfterUpdate(settings: Record<string, string> = {}) {
  localStorage.setItem(WHATS_NEW_STORAGE_KEY, 'an-earlier-release');
  return renderApp({ initialState: { settings } });
}

async function press(daemon: ScriptedDaemon, key: string) {
  await gesture(daemon, () => fireEvent.keyDown(document.activeElement ?? document.body, { key }));
}

async function walkToLastStep(daemon: ScriptedDaemon) {
  const steps = within(whatsNew()!).getAllByRole('button', { name: /^Step \d+:/ }).length;
  for (let i = 1; i < steps; i++) await press(daemon, 'ArrowRight');
}

describe('App what’s new', () => {
  it('greets the first launch after an update with the first step, and never an up-to-date launch', async () => {
    const first = await launchAfterUpdate();
    expect(stepTitle()).toBe('One profile per world');
    expect(within(whatsNew()!).getByText(/^1 of \d+$/)).toBeInTheDocument();

    localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
    await restartApp(first);
    expect(whatsNew()).toBeNull();
  });

  it('steps forward with the arrow, Enter or Next, and back with the arrow or Back', async () => {
    const { daemon } = await launchAfterUpdate();

    await press(daemon, 'ArrowRight');
    expect(stepTitle()).toBe('Desktops arrange your agents');
    await press(daemon, 'Enter');
    expect(stepTitle()).toBe('Arrange each desktop your way');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Next' })));
    expect(stepTitle()).toBe("The queue decides what's next");

    await press(daemon, 'ArrowLeft');
    expect(stepTitle()).toBe('Arrange each desktop your way');
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Back' })));
    expect(stepTitle()).toBe('Desktops arrange your agents');
    await press(daemon, 'ArrowLeft');
    await press(daemon, 'ArrowLeft');
    expect(stepTitle()).toBe('One profile per world');
    expect(screen.queryByRole('button', { name: 'Back' })).toBeNull();
  });

  it('ends on Got it, which stays dismissed across launches', async () => {
    const first = await launchAfterUpdate();
    await walkToLastStep(first.daemon);
    expect(stepTitle()).toBe('Keep your hands on the keyboard');
    expect(screen.queryByRole('button', { name: 'Next' })).toBeNull();

    await press(first.daemon, 'ArrowRight');
    expect(stepTitle()).toBe('Keep your hands on the keyboard');

    await gesture(first.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Got it' })));
    expect(whatsNew()).toBeNull();
    await restartApp(first);
    expect(whatsNew()).toBeNull();
  });

  it('marks the release seen when Escape closes it mid-way', async () => {
    const first = await launchAfterUpdate();
    await press(first.daemon, 'ArrowRight');

    await press(first.daemon, 'Escape');
    expect(whatsNew()).toBeNull();
    await restartApp(first);
    expect(whatsNew()).toBeNull();
  });

  it('hands off to the full shortcut list', async () => {
    const { daemon } = await launchAfterUpdate();

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'View all shortcuts →' })));

    expect(whatsNew()).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Keyboard Shortcuts' })).toBeInTheDocument();
  });

  it('replays from the command palette, starting over', async () => {
    const { daemon } = await launchAfterUpdate();
    await press(daemon, 'ArrowRight');
    await press(daemon, 'Escape');
    expect(whatsNew()).toBeNull();

    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    const search = within(screen.getByRole('dialog')).getByRole('combobox');
    fireEvent.change(search, { target: { value: ">What's new" } });
    await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));

    expect(stepTitle()).toBe('One profile per world');
  });

  it('draws the keycaps the user’s bindings resolve to', async () => {
    const { daemon } = await launchAfterUpdate({
      keybindings_config: JSON.stringify({ version: 1, overrides: { 'profile.switch': { key: 'y', meta: true, alt: true } } }),
    });
    expect(stepKeys()).toEqual(['⌘⌥YSwitch profile']);
    expect(screen.getByTestId('whats-new-scene-press')).toHaveTextContent('⌘⌥Y');

    await press(daemon, 'ArrowRight');
    await press(daemon, 'ArrowRight');
    expect(stepKeys()).toEqual([
      '⌘⇧⏎Focus one pane',
      '⌘⌥1–9Move and follow',
      '⌘⌥⇧1–9Move and stay',
    ]);
    expect(screen.getByTestId('whats-new-scene-press')).toHaveTextContent('⌘⌥2');

    await press(daemon, 'ArrowRight');
    expect(stepKeys()).toEqual([
      '⌘⇧ESettle',
      '⌘↑/⌘↓Step through the queue',
      '⌘JNext waiting agent',
      '⌘⇧JNext automation run',
    ]);
  });

  it('names Linux keys on Linux', async () => {
    onTestFinished(stubNavigatorPlatform('Linux x86_64'));
    const { daemon } = await launchAfterUpdate();
    expect(stepKeys()).toEqual(['CtrlAltUSwitch profile']);

    await press(daemon, 'ArrowRight');
    expect(stepKeys()).toEqual([
      'CtrlShift1–9Switch desktop',
      'CtrlShiftGSee all desktops',
    ]);
  });

  it('keeps its arrow keys away from the terminal underneath', async () => {
    const { daemon } = await openAttachedTerminals({
      sessions: [daemonSession('s1', { state: 'idle' })],
      desktops: [soloDesktop('s1', { shortcut_slot: 1 })],
    });
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    const search = screen.getByRole('combobox');
    fireEvent.change(search, { target: { value: ">What's new" } });
    await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
    const before = daemon.sentOf('pty_input').length;

    await press(daemon, 'ArrowRight');
    await press(daemon, 'ArrowLeft');

    expect(stepTitle()).toBe('One profile per world');
    expect(daemon.sentOf('pty_input').slice(before)).toEqual([]);
  });

  it('offers a replay banner on Home for a new release, which replays from the first step', async () => {
    localStorage.removeItem(WHATS_NEW_BANNER_STORAGE_KEY);
    const { daemon } = await renderApp();
    expect(whatsNew()).toBeNull();

    await gesture(daemon, () => fireEvent.click(within(introBanner()!).getByRole('button', { name: 'Replay the intro' })));

    expect(stepTitle()).toBe('One profile per world');
  });

  it('keeps a dismissed banner away across launches, leaving the command palette as the way back', async () => {
    localStorage.removeItem(WHATS_NEW_BANNER_STORAGE_KEY);
    const first = await renderApp();

    await gesture(first.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Dismiss the intro banner' })));
    expect(introBanner()).toBeNull();

    const { daemon } = await restartApp(first);
    expect(introBanner()).toBeNull();
    await gesture(daemon, () => pressShortcut('ui.commandPalette'));
    const search = within(screen.getByRole('dialog')).getByRole('combobox');
    fireEvent.change(search, { target: { value: ">What's new" } });
    await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));
    expect(stepTitle()).toBe('One profile per world');
  });
});
