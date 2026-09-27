import { fireEvent, screen, within } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import {
  agentPane,
  agentWorkspace,
  crewMember,
  daemonEndpoint,
  daemonSession,
  daemonWorkspace,
  splitWorkspace,
  workspaceWithTiles,
  type DaemonSession,
  type DaemonWorkspace,
} from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp, restartApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';

type SessionPullRequest = NonNullable<DaemonSession['pull_requests']>[number];
type Automation = NonNullable<DaemonSession['automation']>;

const REVIEW_RUN: Automation = {
  run_id: 'run-1',
  definition_id: 'review-sol',
  definition_name: 'Requested PR review - GPT Sol medium',
  trigger_type: 'github_review_requested',
  pull_request: {
    repository: 'ghe.example.net/audiobook/feed-nexus-web',
    number: 101,
    url: 'https://ghe.example.net/audiobook/feed-nexus-web/pull/101',
    title: 'Fix validation race',
    head_sha: '82f1c7a000000000000000000000000000000000',
  },
};

interface Launch {
  sessions?: DaemonSession[];
  workspaces?: DaemonWorkspace[];
  settings?: Record<string, string>;
  crew?: ReturnType<typeof crewMember>[];
}

async function launch({ sessions = [], workspaces = sessions.map((session) => agentWorkspace(session.id)), settings = {}, crew }: Launch) {
  const view = await renderApp({ initialState: { sessions, workspaces, settings, ...(crew ? { crew } : {}) } });
  serveSettings(view.daemon, settings);
  return view;
}

function row(sessionId: string) {
  return within(screen.getByTestId(`sidebar-session-${sessionId}`));
}

function workspaceGroup(workspaceId: string) {
  return screen.getByTestId(`sidebar-workspace-${workspaceId}`);
}

async function openSidebarSettings(daemon: ScriptedDaemon) {
  await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Sidebar settings' })));
  return within(screen.getByRole('dialog', { name: 'Sidebar settings' }));
}

function savedSetting(daemon: ScriptedDaemon, key: string) {
  return daemon.sentOf('set_setting').filter((command) => command.key === key).map(({ value }) => value);
}

const HARNESSES: Array<[string, string, string]> = [
  ['claude-row', 'claude', 'Claude'],
  ['codex-row', 'codex', 'Codex'],
  ['pi-row', 'pi', 'Pi'],
  ['copilot-row', 'copilot', 'Copilot'],
  ['shell-row', 'shell', 'Shell'],
  ['plugin-row', 'custom-driver', 'Custom Driver'],
];

function harnessWorkspace(muted = false) {
  const ids = HARNESSES.map(([id]) => id);
  return daemonWorkspace('ws', {
    root: ids.reduce<unknown>((left, id) => (left
      ? { type: 'split', split_id: `split-${id}`, direction: 'vertical', ratio: 0.5, children: [left, { type: 'pane', pane_id: `pane-${id}` }] }
      : { type: 'pane', pane_id: `pane-${id}` }), null),
    panes: ids.map((id) => agentPane(id, 'ws')),
  }, { title: 'attn', muted });
}

function harnessSessions(overrides: (id: string) => Partial<DaemonSession> = () => ({})) {
  return HARNESSES.map(([id, agent]) => daemonSession(id, { agent, workspace_id: 'ws', state: 'idle', ...overrides(id) }));
}

describe('App sidebar', () => {
  describe('harness identity', () => {
    it.each([false, true])('marks each row with its harness, in a muted workspace too (%s)', async (muted) => {
      const { daemon } = await launch({ sessions: harnessSessions(), workspaces: [harnessWorkspace(muted)] });
      if (muted) await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Muted Workspaces \(1\)/ })));

      for (const [id, , name] of HARNESSES) {
        expect(row(id).getByRole('img', { name })).toHaveAttribute('title', name);
        await gesture(daemon, () => fireEvent.click(row(id).getByRole('button', { name: `Open ${id}` })));
        expect(daemon.sentOf('session_selected').slice(-1)).toEqual([{ cmd: 'session_selected', id }]);
      }
    });

    it('keeps each harness mark in the queue and in the workspace tree, and keeps crew management in both', async () => {
      const crew = [crewMember('fern'), crewMember('sleeping')];
      const sessions = harnessSessions((id) => (id === 'pi-row' ? { crew_member: 'fern' } : {}));
      const { daemon } = await launch({ sessions, workspaces: [harnessWorkspace()], crew, settings: { queue_mode_enabled: 'true' } });

      expect(within(screen.getByTestId('queue-crew-fern')).getByRole('img', { name: 'Pi' })).toBeInTheDocument();
      expect(within(screen.getByTestId('queue-crew-sleeping')).queryByRole('img')).toBeNull();
      expect(screen.getByRole('button', { name: 'Open codex-row' })).toHaveAttribute('title', 'Codex');
      expect(screen.getByTestId('manage-crew')).toHaveTextContent('Manage crew2');

      const settings = await openSidebarSettings(daemon);
      await gesture(daemon, () => fireEvent.click(settings.getByRole('switch', { name: 'Agent queue' })));
      await gesture(daemon, () => fireEvent.keyDown(window, { key: 'Escape' }));

      expect(row('codex-row').getByRole('img', { name: 'Codex' })).toBeInTheDocument();
      expect(screen.getByTestId('manage-crew')).toHaveTextContent('Manage crew2');
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
      expect(screen.getByTestId('crew-panel')).toBeInTheDocument();
    });

    it('keeps the harness as hover text when its logo is turned off', async () => {
      const { daemon } = await launch({ sessions: harnessSessions(), workspaces: [harnessWorkspace()], settings: { queue_mode_enabled: 'true' } });

      const settings = await openSidebarSettings(daemon);
      expect(settings.getByRole('switch', { name: /harness logos/i })).toHaveAttribute('aria-checked', 'true');
      await gesture(daemon, () => fireEvent.click(settings.getByRole('switch', { name: /harness logos/i })));

      expect(savedSetting(daemon, 'sidebar_harness_logos_enabled')).toEqual(['false']);
      expect(screen.getByRole('button', { name: 'Open codex-row' })).toHaveAttribute('title', 'Codex');
    });
  });

  describe('session rows', () => {
    it.each<[string, SessionPullRequest[], string, { text: string; label: string } | null]>([
      ['the open pull request with failing checks', [pr(71, 'open', { ci_status: 'failure' })], 'ledger sweep', { text: '#71', label: 'github.com/victorarias/attn#71 · checks failed' }],
      ['review status only in the tooltip', [pr(71, 'open', { review_status: 'changes_requested' })], 'ledger sweep', { text: '#71', label: 'github.com/victorarias/attn#71 · changes requested' }],
      ['an open pull request over a merged one', [pr(72, 'open'), pr(71, 'merged')], 'ledger sweep', { text: '#72', label: 'github.com/victorarias/attn#72 · open' }],
      ['a merged pull request when none is open', [pr(71, 'merged')], 'ledger sweep', { text: '#71', label: 'github.com/victorarias/attn#71 · merged' }],
      ['the whole number beside a long name', [pr(71, 'open', { ci_status: 'pending' })], 'delegate: rebuild the entire attention ledger projection pipeline', { text: '#71', label: 'github.com/victorarias/attn#71 · checks running' }],
      ['nothing for a closed pull request', [pr(71, 'closed')], 'ledger sweep', null],
      ['nothing without a pull request', [], 'ledger sweep', null],
    ])('shows %s', async (_, pullRequests, label, expected) => {
      await launch({ sessions: [daemonSession('s1', { label, pull_requests: pullRequests })] });

      const entries = Array.from(screen.getByTestId('sidebar-session-s1').querySelectorAll('.sidebar-session-pr'), (entry) => ({
        text: entry.textContent,
        label: entry.getAttribute('aria-label'),
      }));
      expect(entries).toEqual(expected ? [expected] : []);
      expect(row('s1').getByText(label)).toBeInTheDocument();
    });

    it('shows the delegated-from-chief badge only on sessions the chief delegated', async () => {
      await launch({ sessions: [daemonSession('s1', { delegated_from_chief: true }), daemonSession('s2')] });

      expect(row('s1').getByLabelText('Delegated from chief of staff')).toBeInTheDocument();
      expect(row('s2').queryByLabelText('Delegated from chief of staff')).toBeNull();
    });

    it('shows a remote session’s endpoint, and offers close and reload for it', async () => {
      const { daemon } = await renderApp({
        initialState: {
          endpoints: [daemonEndpoint('ep-1')],
          sessions: [daemonSession('remote-1', { endpoint_id: 'ep-1' })],
          workspaces: [agentWorkspace('remote-1')],
        },
      });

      expect(row('remote-1').getByText('gpu-box')).toBeInTheDocument();
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Actions for remote-1' })));
      expect(screen.getByRole('menuitem', { name: /Close session/ })).toBeInTheDocument();
      expect(screen.getByRole('menuitem', { name: /Reload session/ })).toBeInTheDocument();
    });

    it('marks the chief and takes the role away from its menu', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('chief', { chief_of_staff: true })] });

      expect(row('chief').getByLabelText('Chief of staff')).toBeInTheDocument();
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Actions for chief' })));
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('chief-of-staff-session-action')));

      expect(daemon.sentOf('set_chief_of_staff')).toEqual([{ cmd: 'set_chief_of_staff', session_id: 'chief', chief_of_staff: false }]);
    });
  });

  describe('automations', () => {
    it('gathers an automation’s sessions, muted ones included, under one collapsed group after ordinary rows', async () => {
      const second = { ...REVIEW_RUN, run_id: 'run-2' };
      const { daemon } = await launch({
        sessions: [
          daemonSession('manual'),
          daemonSession('run-a', { label: 'feed-nexus-web', automation: REVIEW_RUN }),
          daemonSession('run-b', { label: 'review B', automation: second }),
        ],
        workspaces: [agentWorkspace('manual'), agentWorkspace('run-a'), { ...agentWorkspace('run-b'), muted: true }],
      });
      const header = screen.getByTestId('sidebar-automation-header-review-sol');

      expect(header).toHaveAttribute('aria-expanded', 'false');
      expect(header).toHaveTextContent('Requested PR review - GPT Sol medium');
      expect(header).toHaveTextContent('2 agents');
      expect(screen.queryByTestId('sidebar-session-run-a')).toBeNull();
      expect(screen.queryByRole('button', { name: /Muted Workspaces/ })).toBeNull();
      expect(screen.getByTestId('sidebar-session-manual').compareDocumentPosition(header) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

      await gesture(daemon, () => fireEvent.click(header));

      expect(row('run-a').getByText('GPT Sol medium')).toBeInTheDocument();
      expect(row('run-a').getByText('#101')).toBeInTheDocument();
      expect(screen.getByTestId('sidebar-session-run-b')).toBeInTheDocument();
    });

    it('counts a lone automation session as one agent', async () => {
      await launch({ sessions: [daemonSession('run-a', { automation: REVIEW_RUN })] });

      expect(screen.getByTestId('sidebar-automation-header-review-sol')).toHaveTextContent('1 agent');
    });
  });

  describe('workspaces', () => {
    it('numbers workspace headers in sidebar order, skipping workspaces with nothing in them', async () => {
      await launch({
        sessions: [daemonSession('a1', { workspace_id: 'a' }), daemonSession('a2', { workspace_id: 'a' }), daemonSession('b1', { workspace_id: 'b' })],
        workspaces: [
          daemonWorkspace('empty', { root: { type: 'pane', pane_id: 'pane-nothing' } }, { rank: '0' }),
          splitWorkspace('a', ['a1', 'a2'], { rank: '1' }),
          splitWorkspace('b', ['b1'], { rank: '2' }),
        ],
      });

      expect(screen.queryByTestId('sidebar-workspace-empty')).toBeNull();
      expect(within(workspaceGroup('a')).getByText('⌘1')).toBeInTheDocument();
      expect(within(workspaceGroup('b')).getByText('⌘2')).toBeInTheDocument();
      expect(screen.getByTestId('sidebar-session-a1')).not.toHaveTextContent('⌘1');
    });

    it('reveals tile-only workspaces from Sidebar settings, without a session state, and remembers it', async () => {
      const tileOnly = daemonWorkspace('docs', { root: { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/repo/docs/notes.md' } }, { title: 'docs' });
      const options = { sessions: [daemonSession('a1', { workspace_id: 'workspace-a1' })], workspaces: [agentWorkspace('a1'), tileOnly] };
      const first = await launch(options);
      expect(screen.queryByTestId('sidebar-workspace-docs')).toBeNull();

      const settings = await openSidebarSettings(first.daemon);
      await gesture(first.daemon, () => fireEvent.click(settings.getByTestId('toggle-show-sessionless')));

      expect(within(workspaceGroup('docs')).getByTestId('workspace-neutral-indicator')).toBeInTheDocument();
      expect(workspaceGroup('docs').querySelector('.state-indicator')).toBeNull();
      expect(within(workspaceGroup('workspace-a1')).queryByTestId('workspace-neutral-indicator')).toBeNull();

      await restartApp(first, { initialState: options });
      expect(workspaceGroup('docs')).toBeInTheDocument();
    });

    it('mutes whole workspaces, lists them apart, and unmutes them', async () => {
      const { daemon } = await launch({
        sessions: [daemonSession('s1', { label: 'active' }), daemonSession('s2', { label: 'quiet', state: 'waiting_input' })],
        workspaces: [{ ...agentWorkspace('s1'), title: 'active' }, { ...agentWorkspace('s2'), title: 'quiet', muted: true }],
      });

      expect(screen.queryByRole('button', { name: /Mute session/ })).toBeNull();
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Mute workspace active' })));
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /Muted Workspaces \(1\)/ })));
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Unmute workspace quiet' })));

      expect(daemon.sent.filter(({ cmd }) => cmd.includes('mute'))).toEqual([
        expect.objectContaining({ workspace_id: 'workspace-s1' }),
        expect.objectContaining({ workspace_id: 'workspace-s2' }),
      ]);
    });

    it('lists a workspace’s browser tile after its session, and opens, reloads and closes it', async () => {
      const workspace = workspaceWithTiles([{ tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://www.example.test' }]);
      vi.mocked(isTauri).mockReturnValue(true);
      vi.mocked(invoke).mockResolvedValue(undefined);
      const { daemon } = await launch({ sessions: [daemonSession('s1', { workspace_id: 'ws' })], workspaces: [workspace] });
      const tile = screen.getByTestId('sidebar-tile-ws-tile-browser');
      expect(tile).toHaveTextContent('www.example.test');
      expect(screen.getByTestId('sidebar-session-s1').compareDocumentPosition(tile) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

      await gesture(daemon, () => fireEvent.click(within(tile).getByRole('button', { name: 'Open www.example.test' })));
      expect(daemon.sentOf('workspace_selected').slice(-1)).toEqual([{ cmd: 'workspace_selected', workspace_id: 'ws' }]);

      await gesture(daemon, () => fireEvent.click(within(tile).getByRole('button', { name: 'Reload www.example.test' })));
      expect(vi.mocked(invoke)).toHaveBeenCalledWith('browser_host_control', expect.objectContaining({ label: 'browser-ws-tile-browser', action: 'reload' }));

      await gesture(daemon, () => fireEvent.click(within(tile).getByRole('button', { name: 'Close www.example.test' })));
      expect(daemon.sentOf('workspace_layout_undock_tile')).toEqual([expect.objectContaining({ workspace_id: 'ws', tile_id: 'tile-browser' })]);
    });
  });

  describe('dragging', () => {
    async function twoWorkspaces() {
      const view = await launch({
        sessions: [daemonSession('s1', { workspace_id: 'source' }), daemonSession('s2', { workspace_id: 'source' }), daemonSession('t1', { workspace_id: 'target' })],
        workspaces: [
          splitWorkspace('source', ['s1', 's2'], { rank: '0' }),
          splitWorkspace('target', ['t1'], { rank: '1' }),
        ],
      });
      return view;
    }

    function pressRow(sessionId: string) {
      const select = screen.getByTestId(`sidebar-session-${sessionId}`).querySelector('.sidebar-row-select')!;
      fireEvent.pointerDown(select, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
      return select;
    }

    it('drags a session row onto another workspace, which the source workspace refuses', async () => {
      const { daemon } = await twoWorkspaces();

      pressRow('s2');
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 40 });
      expect(screen.getByTestId('session-drag-ghost')).toHaveTextContent('s2');
      fireEvent.pointerEnter(workspaceGroup('source'));
      fireEvent.pointerUp(workspaceGroup('source'), { pointerId: 1 });
      await daemon.idle();
      expect(daemon.sent.filter(({ cmd }) => cmd.startsWith('workspace_layout_move'))).toEqual([]);

      pressRow('s2');
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 40 });
      fireEvent.pointerEnter(workspaceGroup('target'));
      fireEvent.pointerUp(workspaceGroup('target'), { pointerId: 1 });
      await daemon.idle();

      expect(daemon.sent.filter(({ cmd }) => cmd.startsWith('workspace_layout_move'))).toEqual([
        expect.objectContaining({ source_workspace_id: 'source', leaf_id: 'pane-s2', target_workspace_id: 'target' }),
      ]);
    });

    it('treats a press that barely moves as a click on the row', async () => {
      const { daemon } = await twoWorkspaces();

      const select = pressRow('s2');
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 11, clientY: 12 });
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 11, clientY: 12 });
      await gesture(daemon, () => fireEvent.click(select));

      expect(screen.queryByTestId('session-drag-ghost')).toBeNull();
      expect(daemon.sentOf('session_selected').slice(-1)).toEqual([{ cmd: 'session_selected', id: 's2' }]);
    });
  });

  describe('Sidebar settings', () => {
    it.each([
      ['Agent queue', 'queue_mode_enabled', 'false'],
      ['Crew in queue', 'queue_crew_enabled', 'false'],
    ])('shows %s as the daemon has it and saves a flip', async (name, key, initial) => {
      const { daemon } = await launch({ sessions: [daemonSession('s1')] });

      const settings = await openSidebarSettings(daemon);
      const toggle = settings.getByRole('switch', { name: new RegExp(name, 'i') });
      expect(toggle).toHaveAttribute('aria-checked', initial);
      await gesture(daemon, () => fireEvent.click(toggle));

      expect(savedSetting(daemon, key)).toEqual([initial === 'true' ? 'false' : 'true']);
      expect(settings.getByRole('switch', { name: new RegExp(name, 'i') })).toHaveAttribute('aria-checked', initial === 'true' ? 'false' : 'true');
    });

    it('switches how the selected workspace’s tiles are marked, and remembers it', async () => {
      const options = { sessions: [daemonSession('s1', { state: 'idle' })] };
      const first = await launch(options);

      const settings = await openSidebarSettings(first.daemon);
      expect(settings.getByRole('button', { name: 'rail' })).toHaveAttribute('aria-pressed', 'true');
      await gesture(first.daemon, () => fireEvent.click(settings.getByRole('button', { name: 'dim' })));
      expect(settings.getByRole('button', { name: 'dim' })).toHaveAttribute('aria-pressed', 'true');
      expect(settings.getByRole('button', { name: 'rail' })).toHaveAttribute('aria-pressed', 'false');

      const second = await restartApp(first, { initialState: { ...options, workspaces: [agentWorkspace('s1')] } });
      const reopened = await openSidebarSettings(second.daemon);
      expect(reopened.getByRole('button', { name: 'dim' })).toHaveAttribute('aria-pressed', 'true');
    });

    it('hands focus to the control the user clicks to dismiss it', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('s1')] });
      await openSidebarSettings(daemon);
      const other = screen.getByRole('button', { name: 'Pin workspace s1' });

      other.focus();
      fireEvent.pointerDown(other);
      fireEvent.mouseDown(other);
      await gesture(daemon, () => fireEvent.click(other));

      expect(screen.queryByRole('dialog', { name: 'Sidebar settings' })).toBeNull();
      expect(other).toHaveFocus();
    });
  });

  describe('dock', () => {
    it('shows the configured shortcuts with their keys and runs one on click', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('s1')] });
      const dock = () => Array.from(document.querySelectorAll('.sidebar-dock-items .shortcut-hint'), (item) => item.textContent);

      expect(dock()).toContain('⌘⇧PPRs');
      const dockItems = within(document.querySelector<HTMLElement>('.sidebar-dock-items')!);
      await gesture(daemon, () => fireEvent.click(dockItems.getByRole('button', { name: /PRs/ })));

      expect(document.querySelector('.sidebar-dock-items [data-active="true"]')).toHaveTextContent('PRs');
    });

    it('collapses the dock and remembers it', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('s1')] });

      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Hide dock' })));
      expect(document.querySelector('.sidebar-dock-items')).toBeNull();
      expect(JSON.parse(savedSetting(daemon, 'keybindings_config')[0]).dock.collapsed).toBe(true);

      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Show dock' })));
      expect(document.querySelector('.sidebar-dock-items')).not.toBeNull();
    });
  });

  describe('header', () => {
    it('names no instance in a default build, expanded or collapsed', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('s1')] });
      expect(document.querySelector('.sidebar-header')).not.toHaveTextContent(/instance/i);

      await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
      expect(screen.queryByTitle(/^Instance:/)).toBeNull();
    });
  });

  describe('home', () => {
    it('hints the keys home is bound to', async () => {
      await launch({
        sessions: [daemonSession('s1')],
        settings: { keybindings_config: JSON.stringify({ version: 1, overrides: { 'session.goToDashboard': { key: 'y', meta: true, alt: true } } }) },
      });

      expect(screen.getByTestId('sidebar-home').querySelector('.sidebar-home-shortcut')).toHaveTextContent('⌘⌥Y');
    });

    it('keeps a way home when the sidebar is collapsed, and a waiting workspace still shows its badge', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'waiting_input' })] });
      await gesture(daemon, () => pressShortcut('session.toggleSidebar'));
      const home = () => screen.getByRole('button', { name: 'Home' });

      expect(home()).toHaveAttribute('aria-current', 'page');
      expect(screen.getByTitle(/^s2/).querySelector('.mini-badge')).not.toBeNull();
      expect(screen.getByTitle(/^s1/).querySelector('.mini-badge')).toBeNull();

      await gesture(daemon, () => fireEvent.click(screen.getByTitle(/^s1/)));
      expect(home()).not.toHaveAttribute('aria-current');
      await gesture(daemon, () => fireEvent.click(home()));
      expect(home()).toHaveAttribute('aria-current', 'page');
    });
  });
});

function pr(number: number, state: string, extra: Partial<SessionPullRequest> = {}): SessionPullRequest {
  return {
    repository: 'github.com/victorarias/attn',
    number,
    url: `https://github.com/victorarias/attn/pull/${number}`,
    created_at: '2026-08-30T10:00:00Z',
    state,
    ...extra,
  };
}

