import { act, fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import {
  agentPane,
  daemonSession,
  daemonWorkspace,
  type DaemonSession,
} from './test/daemonFixtures';
import { pressShortcut, renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const WORKSPACE = 'workspace-main';
const COUNTING_DOWN = '2999-01-01T00:00:00.000Z';

function agent(id: string, overrides: Partial<DaemonSession> = {}): DaemonSession {
  return daemonSession(id, {
    workspace_id: WORKSPACE,
    directory: '/tmp/main',
    turn_owed: true,
    turn_opened_at: '2026-08-03T09:00:00Z',
    ...overrides,
  });
}

const splitWorkspace = daemonWorkspace(WORKSPACE, {
  root: {
    type: 'split',
    split_id: 'split-1',
    direction: 'horizontal',
    ratio: 0.5,
    children: [
      { type: 'pane', pane_id: 'pane-s1' },
      { type: 'pane', pane_id: 'pane-s2' },
    ],
  },
  panes: [agentPane('s1', WORKSPACE), agentPane('s2', WORKSPACE)],
});

async function focusFirstPane() {
  const rendered = await renderApp({
    initialState: {
      sessions: [agent('s1'), agent('s2')],
      workspaces: [splitWorkspace],
      settings: { queue_mode_enabled: 'true' },
    },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Open s1' }));
  return rendered;
}

function pressCancelCountdown(daemon: ScriptedDaemon): string[] {
  const before = daemon.sent.length;
  act(() => {
    window.dispatchEvent(new CustomEvent('attn:native-shortcut', { detail: 'session.cancelCountdown' }));
  });
  return daemon.sent
    .slice(before)
    .flatMap((command) => (command.cmd === 'cancel_countdown' ? [command.session_id] : []));
}

describe('who ⌘. names', () => {
  it('names the focused session alone when nothing is counting down', async () => {
    const { daemon } = await focusFirstPane();

    expect(pressCancelCountdown(daemon)).toEqual(['s1']);
  });

  it('names every visible countdown instead, wherever it is running', async () => {
    const { daemon } = await focusFirstPane();
    daemon.emit({
      event: 'session_state_changed',
      session: agent('s2', { auto_settle_fires_at: COUNTING_DOWN }),
    });

    expect(pressCancelCountdown(daemon)).toEqual(['s2']);
  });

  it('names nothing when no tile is on screen at all', async () => {
    const { daemon } = await focusFirstPane();

    pressShortcut('session.goToDashboard');
    expect(screen.getByTestId('sidebar-home')).toHaveAttribute('aria-current', 'page');

    expect(pressCancelCountdown(daemon)).toEqual([]);
  });
});
