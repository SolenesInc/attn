import { act, fireEvent, screen, within } from '@testing-library/react';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { describe, expect, it, vi } from 'vitest';
import { agentWorkspace, daemonSession, type DaemonSession } from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const S1_TURN_OPENED = '2026-08-03T09:00:00Z';
const S2_TURN_OPENED = '2026-08-03T10:00:00Z';

function agent(id: 's1' | 's2', overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, {
    turn_opened_at: id === 's1' ? S1_TURN_OPENED : S2_TURN_OPENED,
    ...overrides,
  });
}

async function renderQueue({
  owed = [] as string[],
  laidOut = ['s1', 's2'],
  s1 = {} as Partial<DaemonSession>,
} = {}) {
  return renderApp({
    initialState: {
      sessions: [
        agent('s1', { turn_owed: owed.includes('s1'), ...s1 }),
        agent('s2', { turn_owed: owed.includes('s2') }),
      ],
      workspaces: laidOut.map(agentWorkspace),
      settings: { queue_mode_enabled: 'true' },
    },
  });
}

function press(key: string, modifiers: { shift?: boolean } = {}) {
  fireEvent.keyDown(window, { key, metaKey: true, shiftKey: modifiers.shift ?? false });
}

const keys = {
  home: () => press('H', { shift: true }),
  back: () => press('['),
  forward: () => press(']'),
  grid: () => press('g'),
  sidebar: () => press('B', { shift: true }),
  settings: () => press(','),
  shortcuts: () => press('/'),
  sessions: () => press('L', { shift: true }),
};

function open(label: string) {
  fireEvent.click(screen.getByRole('button', { name: `Open ${label}` }));
}

function selectedAgent(): string | null {
  return document.querySelector('.session-item.selected .session-label')?.textContent ?? null;
}

function isHome(): boolean {
  return screen.getByTestId('sidebar-home').getAttribute('aria-current') === 'page';
}

function isGrid(): boolean {
  return screen.queryByRole('region', { name: 'Session grid' }) !== null;
}

function setTurn(daemon: ScriptedDaemon, id: 's1' | 's2', owed: boolean) {
  daemon.emit({ event: 'session_state_changed', session: agent(id, { turn_owed: owed }) });
}

function layOut(daemon: ScriptedDaemon, id: string) {
  daemon.emit({ event: 'workspace_state_changed', workspace: agentWorkspace(id) });
}

function deepLinkTo(id: string) {
  act(() => vi.mocked(onOpenUrl).mock.lastCall![0]([`attn://spawn?cwd=%2Ftmp%2F${id}`]));
}

function selectionsOf(daemon: ScriptedDaemon, workspaceId: string) {
  return daemon.sent.filter(
    (command) => command.cmd === 'workspace_selected' && command.workspace_id === workspaceId,
  );
}

async function workTheQueueDownToHome() {
  const rendered = await renderQueue({ owed: ['s1'] });
  open('s1');
  expect(selectedAgent()).toBe('s1');

  setTurn(rendered.daemon, 's1', false);
  expect(isHome()).toBe(true);
  return rendered;
}

describe('agent navigation', () => {
  it('selects a deferred session when its pane becomes available', async () => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });

    deepLinkTo('s2');
    expect(isHome()).toBe(true);

    layOut(daemon, 's2');

    expect(selectedAgent()).toBe('s2');
  });

  it('keeps a newer selection when a deferred one becomes ready', async () => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });

    deepLinkTo('s2');
    open('s1');
    layOut(daemon, 's2');

    expect(selectedAgent()).toBe('s1');
  });

  it('does not leave home when an older deferred selection becomes ready', async () => {
    const { daemon } = await renderQueue({ laidOut: ['s1'] });

    deepLinkTo('s2');
    keys.home();
    layOut(daemon, 's2');

    expect(isHome()).toBe(true);
    expect(selectionsOf(daemon, 'workspace-s2')).toEqual([]);
  });

  it.each([
    ['going home', keys.home],
    ['going back', keys.back],
    ['toggling the sidebar', keys.sidebar],
    ['opening settings', keys.settings],
    ['opening the shortcuts', keys.shortcuts],
    ['opening the sessions list', keys.sessions],
  ])('dismisses the delegation chain when %s', async (_, shortcut) => {
    await renderQueue({ s1: { delegation_role: { name: 'Builder' } } });
    open('s2');
    open('s1');
    fireEvent.click(within(screen.getByTestId('sidebar-queue')).getByTestId('delegation-chain-trigger-s1'));
    expect(screen.getByRole('dialog', { name: 'Delegation chain' })).toBeInTheDocument();

    shortcut();

    expect(screen.queryByRole('dialog', { name: 'Delegation chain' })).toBeNull();
  });

  it('takes the user to the next turn that opens after the queue ran dry', async () => {
    const { daemon } = await workTheQueueDownToHome();

    setTurn(daemon, 's2', true);

    expect(selectedAgent()).toBe('s2');
  });

  it('leaves the user alone at a home they walked to', async () => {
    const { daemon } = await renderQueue({ owed: ['s1'] });
    open('s1');
    keys.home();
    expect(isHome()).toBe(true);

    setTurn(daemon, 's2', true);

    expect(isHome()).toBe(true);
    expect(selectionsOf(daemon, 'workspace-s2')).toEqual([]);
  });

  it('ends the wait when the user leaves home, however they come back', async () => {
    const { daemon } = await workTheQueueDownToHome();
    keys.grid();
    keys.grid();
    expect(isHome()).toBe(true);

    setTurn(daemon, 's2', true);

    expect(isHome()).toBe(true);
    expect(selectionsOf(daemon, 'workspace-s2')).toEqual([]);
  });

  it('hands over the oldest owed turn when several opened while home waited', async () => {
    const { daemon } = await workTheQueueDownToHome();

    daemon.emit({
      event: 'sessions_updated',
      sessions: [agent('s1', { turn_owed: true }), agent('s2', { turn_owed: true })],
    });

    expect(selectedAgent()).toBe('s1');
  });

  it('resumes history from home and grid, then traverses normally in the session view', async () => {
    await renderQueue();
    open('s1');
    open('s2');
    keys.home();

    keys.back();
    expect(selectedAgent()).toBe('s2');
    expect(isGrid()).toBe(false);

    keys.grid();
    keys.forward();
    expect(selectedAgent()).toBe('s2');
    expect(isGrid()).toBe(true);

    keys.back();
    expect(selectedAgent()).toBe('s2');
    expect(isGrid()).toBe(false);

    keys.back();
    expect(selectedAgent()).toBe('s1');

    keys.forward();
    expect(selectedAgent()).toBe('s2');
    keys.forward();
    expect(selectedAgent()).toBe('s2');
  });
});
