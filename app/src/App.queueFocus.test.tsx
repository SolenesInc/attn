import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { crewMember, daemonDesktop, daemonSession, soloDesktop, type DaemonSession } from './test/daemonFixtures';
import { fakeRects } from './test/layout';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const QUEUE = { queue_mode_enabled: 'true' };

function agent(id: string, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, {
    state: 'idle',
    turn_opened_at: id === 's1' ? '2026-08-03T09:00:00Z' : '2026-08-03T10:00:00Z',
    ...overrides,
  });
}

async function launch({
  owed = [] as string[],
  chief = null as string | null,
  settings = QUEUE as Record<string, string>,
  crew = [] as ReturnType<typeof crewMember>[],
} = {}) {
  const sessions = ['s1', 's2'].map((id) => agent(id, { turn_owed: owed.includes(id), chief_of_staff: id === chief }));
  return renderApp({ initialState: { settings, sessions, desktops: sessions.map((session) => soloDesktop(session.id)), crew } });
}

async function openS2(daemon: ScriptedDaemon) {
  const agentList = screen.queryByRole('button', { name: /more agents/i });
  if (!screen.queryByRole('button', { name: 'Open s2' }) && agentList) await gesture(daemon, () => fireEvent.click(agentList));
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s2' })));
}

async function settle(daemon: ScriptedDaemon) {
  await gesture(daemon, () => pressShortcut('session.settle', document.activeElement ?? window));
}

async function snooze(daemon: ScriptedDaemon) {
  await gesture(daemon, () => pressShortcut('session.snooze', document.activeElement ?? window));
}

const snoozeMenu = () => screen.queryByRole('menu', { name: /^Snooze/ });
const agentListOpen = () => screen.queryByTestId('queue-agent-list') !== null;
const agentPalette = () => screen.queryByTestId('palette-agent-s1');
const terminalInput = () => document.querySelector<HTMLElement>('[data-session-visible="1"] [role="textbox"][aria-label="Terminal input"]');

async function hoverWaitingPill(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.pointerEnter(screen.getByTestId('queue-bar-waiting')));
  return document.querySelector('[data-testid="queue-bar-waiting"] .queue-bar-peek');
}

describe('acting on the queue sidebar row that holds focus', () => {
  it('settles the focused row rather than the active agent', async () => {
    const { daemon } = await launch({ owed: ['s1', 's2'] });
    await openS2(daemon);

    screen.getByTestId('queue-select-s1').focus();
    await settle(daemon);

    expect(daemon.sentOf('settle_turn')).toEqual([{ cmd: 'settle_turn', session_id: 's1' }]);
  });

  it('settles nothing when the focused row owes no turn', async () => {
    const { daemon } = await launch({ owed: ['s2'] });
    await openS2(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /more agents/i })));

    screen.getByTestId('queue-select-s1').focus();
    await settle(daemon);

    expect(daemon.sentOf('settle_turn')).toEqual([]);
  });

  it('settles the active agent when no row holds focus', async () => {
    const { daemon } = await launch({ owed: ['s2'] });
    await openS2(daemon);

    (document.activeElement as HTMLElement | null)?.blur();
    await settle(daemon);

    expect(daemon.sentOf('settle_turn')).toEqual([{ cmd: 'settle_turn', session_id: 's2' }]);
  });

  it('acts on no agent while a tile holds the surface and no row holds focus', async () => {
    const { daemon } = await launch({ owed: ['s2'] });
    await openS2(daemon);
    daemon.arrange((desktops) => desktops.map((desktop) => (desktop.id === 'desktop-s2'
      ? {
        ...daemonDesktop('desktop-s2', {
          root: { type: 'split', split_id: 'split-notes', direction: 'vertical', ratio: 0.5, children: [{ type: 'pane', pane_id: 'pane-s2' }, { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/tmp/notes.md' }] },
          panes: desktop.panes,
        }),
        active_pane_id: 'tile-notes',
        revision: desktop.revision + 1,
      }
      : desktop)));
    await daemon.idle();

    (document.activeElement as HTMLElement | null)?.blur();
    await settle(daemon);
    await snooze(daemon);

    expect(daemon.sentOf('settle_turn')).toEqual([]);
    expect(snoozeMenu()).toBeNull();
  });

  it('snoozes the focused row rather than the active agent', async () => {
    const { daemon } = await launch({ owed: ['s1'] });
    await openS2(daemon);

    screen.getByTestId('queue-select-s1').focus();
    await snooze(daemon);

    expect(screen.getByRole('menu', { name: 'Snooze s1' })).toBeInTheDocument();
  });

  it('opens the snooze menu beside the focused copy of an agent listed twice', async () => {
    const { daemon } = await launch({ owed: ['s1'] });
    await openS2(daemon);
    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /more agents/i })));
    const copies = screen.getAllByTestId(/^queue-(turn|settled)-s1$/);
    const focusedCopy = copies[copies.length - 1];
    fakeRects((element) => (element === focusedCopy ? DOMRect.fromRect({ x: 40, y: 280, width: 180, height: 20 }) : null));

    focusedCopy.querySelector<HTMLElement>('.queue-row-select')!.focus();
    await snooze(daemon);

    const menu = screen.getByRole('menu', { name: 'Snooze s1' });
    expect(menu.style.top).toBe('304px');
    expect(menu.style.left).toBe('40px');
  });

  it('acts on no agent from a focused row that holds none, like a sleeping crew member', async () => {
    const { daemon } = await launch({ owed: ['s2'], crew: [crewMember('fern')] });
    await openS2(daemon);

    screen.getByTestId('queue-crew-select-fern').focus();
    await settle(daemon);
    await snooze(daemon);

    expect(daemon.sentOf('settle_turn')).toEqual([]);
    expect(snoozeMenu()).toBeNull();
  });

  it('never snoozes the chief, focused or active', async () => {
    const { daemon } = await launch({ chief: 's1' });
    await openS2(daemon);

    screen.getByTestId('queue-select-s1').focus();
    await snooze(daemon);
    expect(snoozeMenu()).toBeNull();

    daemon.emit({ event: 'sessions_updated', sessions: [agent('s1'), agent('s2', { chief_of_staff: true })] });
    (document.activeElement as HTMLElement | null)?.blur();
    await snooze(daemon);
    expect(snoozeMenu()).toBeNull();
  });
});

describe('the agent list and the agent palette', () => {
  it('toggles the agent list in the open queue sidebar, and opens the palette on agents otherwise', async () => {
    const { daemon } = await launch();

    expect(agentListOpen()).toBe(false);
    await gesture(daemon, () => pressShortcut('sidebar.agentList'));
    expect(agentListOpen()).toBe(true);
    await gesture(daemon, () => pressShortcut('sidebar.agentList'));
    expect(agentListOpen()).toBe(false);
    expect(agentPalette()).toBeNull();

    daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'false' } });
    await gesture(daemon, () => pressShortcut('sidebar.agentList'));
    expect(agentListOpen()).toBe(false);
    expect(agentPalette()).toBeInTheDocument();
  });

  it('hides the sidebar while an agent is focused and opens the palette on agents instead', async () => {
    const { daemon, container } = await launch();
    await openS2(daemon);

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Focus agent s2' })));
    expect(container.querySelector('.app')).toHaveClass('is-agent-focused');
    await gesture(daemon, () => pressShortcut('sidebar.agentList'));
    expect(agentListOpen()).toBe(false);
    expect(agentPalette()).toBeInTheDocument();
  });

  it('opens the palette on agents in grid view, where the grid covers the queue sidebar', async () => {
    const { daemon, container } = await launch();

    await gesture(daemon, () => pressShortcut('view.toggleGrid'));
    expect(container.querySelector('.app')).toHaveClass('is-grid');
    await gesture(daemon, () => pressShortcut('sidebar.agentList'));

    expect(agentListOpen()).toBe(false);
    expect(agentPalette()).toBeInTheDocument();
  });
});

describe('handing focus to the terminal', () => {
  it('hands focus to the terminal when the sidebar hides with focus inside it', async () => {
    const { daemon } = await launch();
    await openS2(daemon);

    screen.getByTestId('queue-select-s2').focus();
    expect(document.activeElement).toBe(screen.getByTestId('queue-select-s2'));
    await gesture(daemon, () => pressShortcut('session.toggleSidebar', document.activeElement!));

    expect(terminalInput()).not.toBeNull();
    expect(document.activeElement).toBe(terminalInput());
  });

  it('hands focus to the terminal when the queue switch swaps the sidebar under focus', async () => {
    const { daemon } = await launch();
    await openS2(daemon);

    screen.getByTestId('queue-select-s2').focus();
    daemon.emit({ event: 'settings_updated', settings: { queue_mode_enabled: 'false' } });
    await daemon.idle();

    expect(terminalInput()).not.toBeNull();
    expect(document.activeElement).toBe(terminalInput());
  });
});

describe('the collapsed queue bar', () => {
  async function collapsed() {
    const view = await launch({ owed: ['s1'] });
    await gesture(view.daemon, () => pressShortcut('session.toggleSidebar'));
    expect(await hoverWaitingPill(view.daemon)).not.toBeNull();
    await gesture(view.daemon, () => fireEvent.pointerLeave(screen.getByTestId('queue-bar-waiting')));
    return view;
  }

  it('opens the palette on agents from the bar, and silences the bar peeks while it is open', async () => {
    const { daemon } = await collapsed();

    await gesture(daemon, () => pressShortcut('sidebar.agentList'));
    expect(agentPalette()).toBeInTheDocument();
    expect(await hoverWaitingPill(daemon)).toBeNull();

    await gesture(daemon, () => fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' }));
    expect(agentPalette()).toBeNull();
    await gesture(daemon, () => fireEvent.pointerLeave(screen.getByTestId('queue-bar-waiting')));
    expect(await hoverWaitingPill(daemon)).not.toBeNull();

    await gesture(daemon, () => fireEvent.click(screen.getByTestId('queue-bar-pill')));
    expect(agentPalette()).toBeInTheDocument();
  });

  it('silences the bar peeks while the Markdown opener covers the window', async () => {
    const { daemon } = await collapsed();

    await gesture(daemon, () => pressShortcut('file.open'));

    expect(await hoverWaitingPill(daemon)).toBeNull();
  });

  it('silences the bar peeks while the grid hides the bar', async () => {
    const { daemon } = await collapsed();

    await gesture(daemon, () => pressShortcut('view.toggleGrid'));

    expect(screen.queryByTestId('queue-bar-waiting') === null || (await hoverWaitingPill(daemon)) === null).toBe(true);
  });
});
