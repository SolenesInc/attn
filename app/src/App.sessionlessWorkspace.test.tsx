import { act, fireEvent, screen } from '@testing-library/react';
import { onOpenUrl } from '@tauri-apps/plugin-deep-link';
import { beforeEach, describe, expect, it, onTestFinished, vi } from 'vitest';
import { SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY } from './application/appSupport';
import {
  agentPane,
  daemonSession,
  daemonWorkspace,
  type DaemonPane,
  type DaemonSession,
  type DaemonWorkspace,
} from './test/daemonFixtures';
import { renderApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { WARM_WORKSPACE_LIMIT_STORAGE_KEY } from './utils/terminalVirtualization';

function repoSession(id: string, workspaceId: string, overrides: Partial<DaemonSession> = {}) {
  return daemonSession(id, { directory: '/tmp/repo', workspace_id: workspaceId, ...overrides });
}

function loneAgentWorkspace(id: string, title: string, sessionId: string, paneId = `pane-${sessionId}`) {
  return daemonWorkspace(
    id,
    { root: { type: 'pane', pane_id: paneId }, panes: [{ ...agentPane(sessionId, id), pane_id: paneId }] },
    { title, directory: '/tmp/repo' },
  );
}

function tileWorkspace(id: string, title: string, tile: Record<string, string>): DaemonWorkspace {
  return daemonWorkspace(id, { root: { type: 'tile', ...tile } }, { title, directory: '/tmp/repo' });
}

const sessionWorkspace = loneAgentWorkspace('ws-session', 'working-session', 's1');
const notesWorkspace = tileWorkspace('ws-tiles', 'Notes', {
  tile_id: 'tile-readme',
  tile_kind: 'markdown',
  tile_params: '/tmp/project/README.md',
});

function renderSessionAndNotes(sessionOverrides: Partial<DaemonSession> = {}) {
  return renderApp({
    initialState: {
      sessions: [repoSession('s1', 'ws-session', { label: 'working-session', ...sessionOverrides })],
      workspaces: [sessionWorkspace, notesWorkspace],
    },
  });
}

function workspaceElement(id: string) {
  return document.querySelector<HTMLElement>(`.session-terminal-workspace[data-workspace-id="${id}"]`);
}

function isShown(id: string) {
  return workspaceElement(id)?.getAttribute('data-session-visible') === '1';
}

function leavesOf(id: string) {
  return Array.from(workspaceElement(id)?.querySelectorAll('[data-pane-id]') ?? []).map((leaf) => ({
    id: leaf.getAttribute('data-pane-id'),
    kind: leaf.getAttribute('data-pane-kind'),
  }));
}

function selectedSidebarRows() {
  return Array.from(document.querySelectorAll('.sidebar .selected')).map((row) =>
    row.getAttribute('data-testid'),
  );
}

function open(name: string) {
  fireEvent.click(screen.getByRole('button', { name }));
}

function lastSelectedWorkspace(daemon: ScriptedDaemon) {
  const selections = daemon.sent.flatMap((command) =>
    command.cmd === 'workspace_selected' ? [command.workspace_id] : [],
  );
  return selections[selections.length - 1];
}

function isLive(paneId: string) {
  return screen.queryByTestId(`pane-virtualized-${paneId}`) === null;
}

beforeEach(() => {
  localStorage.setItem(SHOW_SESSIONLESS_WORKSPACES_STORAGE_KEY, '1');
});

describe('tile-only (sessionless) workspace selection and render', () => {
  it('renders the tile-only workspace layout but leaves it inactive until selected', async () => {
    await renderSessionAndNotes();
    open('Open working-session');

    expect(leavesOf('ws-tiles')).toEqual([{ id: 'tile-readme', kind: 'tile' }]);
    expect(isShown('ws-session')).toBe(true);
    expect(isShown('ws-tiles')).toBe(false);
  });

  it('activates the tile-only workspace when it is selected from the sidebar', async () => {
    const { daemon } = await renderSessionAndNotes();
    open('Open working-session');

    open('Open workspace Notes');

    expect(isShown('ws-tiles')).toBe(true);
    expect(isShown('ws-session')).toBe(false);
    expect(selectedSidebarRows()).toContain('sidebar-workspace-ws-tiles');
    expect(selectedSidebarRows()).not.toContain('sidebar-workspace-ws-session');
    expect(lastSelectedWorkspace(daemon)).toBe('ws-tiles');
    expect(leavesOf('ws-tiles')).toEqual([{ id: 'tile-readme', kind: 'tile' }]);
  });

  it('releases a focused agent pane while a tile-only workspace is selected', async () => {
    await renderSessionAndNotes();
    open('Open working-session');
    open('Focus agent working-session');
    expect(workspaceElement('ws-session')).toHaveAttribute('data-maximized-pane-id', 'pane-s1');

    open('Open workspace Notes');
    expect(workspaceElement('ws-session')).toHaveAttribute('data-maximized-pane-id', '');

    open('Open workspace working-session');
    expect(isShown('ws-session')).toBe(true);
    expect(selectedSidebarRows()).toContain('sidebar-session-s1');
  });

  it('keeps the sidebar expanded for failed panes whose sessions are no longer live', async () => {
    const failedPane = (paneId: string, sessionId: string): DaemonPane => ({
      ...agentPane(sessionId, 'ws-failed'),
      pane_id: paneId,
      title: 'Failed automation',
      status: 'failed',
      error: 'launch failed',
    });
    const { daemon } = await renderApp({
      initialState: {
        workspaces: [
          daemonWorkspace(
            'ws-failed',
            {
              root: {
                type: 'split',
                split_id: 'split-failed',
                direction: 'vertical',
                ratio: 0.5,
                children: [
                  { type: 'pane', pane_id: 'pane-failed' },
                  { type: 'pane', pane_id: 'pane-retry' },
                ],
              },
              panes: [failedPane('pane-failed', 'closed-session'), failedPane('pane-retry', 'closed-retry')],
            },
            { title: 'Failed automation', directory: '/tmp/repo' },
          ),
        ],
      },
    });

    expect(screen.getByRole('button', { name: 'Collapse sidebar' })).toBeInTheDocument();

    open('Open workspace Failed automation');
    const pane = document.querySelector<HTMLElement>('[data-pane-id="pane-failed"]')!;
    fireEvent.keyDown(pane, { key: 'w', metaKey: true });

    expect(daemon.sent.filter((command) => command.cmd === 'workspace_layout_close_pane')).toEqual([
      expect.objectContaining({ workspace_id: 'ws-failed', pane_id: 'pane-failed' }),
    ]);
  });

  it('uses sessions loaded after mount when an existing-session deep link arrives', async () => {
    const { daemon } = await renderApp();

    daemon.emit({
      event: 'sessions_updated',
      sessions: [repoSession('s1', 'ws-session', { label: 'working-session' })],
    });
    daemon.emit({ event: 'workspace_state_changed', workspace: sessionWorkspace });
    act(() => vi.mocked(onOpenUrl).mock.lastCall![0](['attn://spawn?cwd=%2Ftmp%2Frepo']));

    expect(isShown('ws-session')).toBe(true);
    expect(selectedSidebarRows()).toContain('sidebar-session-s1');
    expect(daemon.sent.some((command) => command.cmd === 'register_workspace')).toBe(false);
  });

  it('waits for an opened tile to reach workspace state before selecting and focusing it', async () => {
    const { daemon } = await renderSessionAndNotes({ seed_id: 's-work11' });
    daemon.on('open_seed', (command) => ({
      event: 'open_seed_result',
      seed_id: command.seed_id,
      success: true,
      workspace_id: 'ws-late',
      tile_id: 'tile-seed',
    }));
    open('Open working-session');

    fireEvent.click(screen.getByTestId('seed-chip-s1'));
    await act(async () => {
      await daemon.received('open_seed');
    });
    expect(workspaceElement('ws-late')).toBeNull();
    expect(isShown('ws-session')).toBe(true);

    daemon.emit({
      event: 'workspace_state_changed',
      workspace: tileWorkspace('ws-late', 'Seed reader', {
        tile_id: 'tile-seed',
        tile_kind: 'document',
        tile_params: 'seed:s-work11',
      }),
    });
    act(() => {
      vi.advanceTimersToNextFrame();
    });

    expect(isShown('ws-late')).toBe(true);
    expect(workspaceElement('ws-late')).toHaveAttribute('data-active-leaf-id', 'tile-seed');
    expect(selectedSidebarRows()).toContain('sidebar-tile-ws-late-tile-seed');
    expect(lastSelectedWorkspace(daemon)).toBe('ws-late');
  });

  it('keeps visible grid workspaces mounted even when they are cold and idle', async () => {
    localStorage.setItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY, '0');
    onTestFinished(() => localStorage.removeItem(WARM_WORKSPACE_LIMIT_STORAGE_KEY));
    const names = ['one', 'two', 'three'];
    await renderApp({
      initialState: {
        sessions: names.map((name, index) =>
          repoSession(`s${index + 1}`, `ws-${name}`, { label: name, state: 'idle' }),
        ),
        workspaces: names.map((name, index) =>
          loneAgentWorkspace(`ws-${name}`, name, `s${index + 1}`, `pane-${name}`),
        ),
      },
    });
    open('Open one');
    expect(isLive('pane-two')).toBe(false);

    fireEvent.keyDown(window, { key: 'g', metaKey: true });

    expect(screen.getByRole('region', { name: 'Session grid' })).toBeInTheDocument();
    expect(isLive('pane-two')).toBe(true);
    expect(isLive('pane-three')).toBe(true);
  });
});
