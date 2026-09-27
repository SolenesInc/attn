import { fireEvent, screen, within } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import {
  agentPane,
  soloDesktop,
  crewMember,
  daemonEndpoint,
  daemonSession,
  daemonDesktop,
  splitDesktop,
  desktopWithTiles,
  type DaemonSession,
  type DaemonDesktop,
} from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp, restartApp } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';
import { openActionMenu } from './test/appFixtures';

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
  desktops?: DaemonDesktop[];
  settings?: Record<string, string>;
  crew?: ReturnType<typeof crewMember>[];
}

async function launch({ sessions = [], desktops = sessions.map((session) => soloDesktop(session.id)), settings = {}, crew }: Launch) {
  const view = await renderApp({ initialState: { sessions, desktops, settings, ...(crew ? { crew } : {}) } });
  serveSettings(view.daemon, settings);
  return view;
}

function row(sessionId: string) {
  return within(screen.getByTestId(`sidebar-session-${sessionId}`));
}

function desktopGroup(desktopId: string) {
  return screen.getByTestId(`sidebar-desktop-${desktopId}`);
}

function shownPane() {
  return document.querySelector('[data-session-visible="1"]')?.getAttribute('data-active-pane-id') ?? null;
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

function harnessDesktop() {
  const ids = HARNESSES.map(([id]) => id);
  return daemonDesktop('ws', {
    root: ids.reduce<unknown>((left, id) => (left
      ? { type: 'split', split_id: `split-${id}`, direction: 'vertical', ratio: 0.5, children: [left, { type: 'pane', pane_id: `pane-${id}` }] }
      : { type: 'pane', pane_id: `pane-${id}` }), null),
    panes: ids.map((id) => agentPane(id, 'ws')),
  }, { name: 'attn' });
}

function harnessSessions(overrides: (id: string) => Partial<DaemonSession> = () => ({})) {
  return HARNESSES.map(([id, agent]) => daemonSession(id, { agent, state: 'idle', ...overrides(id) }));
}

describe('App sidebar', () => {
  describe('harness identity', () => {
    it('marks each row with its harness', async () => {
      const { daemon } = await launch({ sessions: harnessSessions(), desktops: [harnessDesktop()] });

      for (const [id, , name] of HARNESSES) {
        expect(row(id).getByRole('img', { name })).toHaveAttribute('title', name);
        await gesture(daemon, () => fireEvent.click(row(id).getByRole('button', { name: `Open ${id}` })));
        expect(shownPane()).toBe(`pane-${id}`);
      }
    });

    it('keeps each harness mark in the queue and in the desktop tree, and keeps crew management in both', async () => {
      const crew = [crewMember('fern'), crewMember('sleeping')];
      const sessions = harnessSessions((id) => (id === 'pi-row' ? { crew_member: 'fern' } : {}));
      const { daemon } = await launch({ sessions, desktops: [harnessDesktop()], crew, settings: { queue_mode_enabled: 'true' } });

      expect(within(screen.getByTestId('queue-crew-fern')).getByRole('img', { name: 'Pi' })).toBeInTheDocument();
      expect(within(screen.getByTestId('queue-crew-sleeping')).queryByRole('img')).toBeNull();
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /All agents/ })));
      expect(screen.getByRole('button', { name: 'Open codex-row' })).toHaveAttribute('title', 'Codex');
      expect(screen.getByTestId('manage-crew')).toHaveTextContent('Manage crew2');

      const search = await openActionMenu(daemon);
      fireEvent.change(search, { target: { value: '>turn off the agent queue' } });
      await gesture(daemon, () => fireEvent.keyDown(search, { key: 'Enter' }));

      expect(row('codex-row').getByRole('img', { name: 'Codex' })).toBeInTheDocument();
      expect(screen.getByTestId('manage-crew')).toHaveTextContent('Manage crew2');
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('manage-crew')));
      expect(screen.getByTestId('crew-panel')).toBeInTheDocument();
    });

    it('turns harness logos off from Sidebar settings', async () => {
      const { daemon } = await launch({ sessions: harnessSessions(), desktops: [harnessDesktop()] });

      const settings = await openSidebarSettings(daemon);
      expect(settings.getByRole('switch', { name: /harness logos/i })).toHaveAttribute('aria-checked', 'true');
      await gesture(daemon, () => fireEvent.click(settings.getByRole('switch', { name: /harness logos/i })));

      expect(savedSetting(daemon, 'sidebar_harness_logos_enabled')).toEqual(['false']);
    });

    it('keeps the harness as hover text in the queue when its logo is turned off', async () => {
      const { daemon } = await launch({
        sessions: harnessSessions(),
        desktops: [harnessDesktop()],
        settings: { queue_mode_enabled: 'true', sidebar_harness_logos_enabled: 'false' },
      });

      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: /All agents/ })));
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

    it.each<[string, string, string | null]>([
      ['stuck', 'Stuck — the agent has stopped reporting anything at all', 'Stuck — the agent has stopped reporting anything at all'],
      ['no_evidence', 'No signal from this agent yet', 'No signal from this agent yet'],
      ['some_future_clause', 'state unknown', null],
    ])('explains an unknown state from the daemon’s %s reason, and invents nothing for a reason it does not know', async (reason, label, hover) => {
      await launch({ sessions: [daemonSession('s1', { state: 'unknown', state_reason: reason })] });

      const indicator = row('s1').getByLabelText(label);
      expect(indicator.getAttribute('title')).toBe(hover);
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
          desktops: [soloDesktop('remote-1')],
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
    it('gathers an automation’s sessions under one collapsed group after ordinary rows', async () => {
      const second = { ...REVIEW_RUN, run_id: 'run-2' };
      const { daemon } = await launch({
        sessions: [
          daemonSession('manual'),
          daemonSession('run-a', { label: 'feed-nexus-web', automation: REVIEW_RUN }),
          daemonSession('run-b', { label: 'review B', automation: second }),
        ],
        desktops: [soloDesktop('manual'), soloDesktop('run-a'), soloDesktop('run-b')],
      });
      const header = screen.getByTestId('sidebar-automation-header-review-sol');

      expect(header).toHaveAttribute('aria-expanded', 'false');
      expect(header).toHaveTextContent('Requested PR review - GPT Sol medium');
      expect(header.querySelector('.automation-session-count')).toHaveTextContent('2');
      expect(screen.queryByTestId('sidebar-session-run-a')).toBeNull();
      expect(screen.getByTestId('sidebar-session-manual').compareDocumentPosition(header) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

      await gesture(daemon, () => fireEvent.click(header));

      expect(row('run-a').getByText('GPT Sol medium')).toBeInTheDocument();
      expect(row('run-a').getByText('#101')).toBeInTheDocument();
      expect(screen.getByTestId('sidebar-session-run-b')).toBeInTheDocument();
    });

    it('counts a lone automation session as one agent', async () => {
      await launch({ sessions: [daemonSession('run-a', { automation: REVIEW_RUN })] });

      expect(screen.getByTestId('sidebar-automation-header-review-sol').querySelector('.automation-session-count')).toHaveTextContent('1');
    });
  });

  describe('desktops', () => {
    it('shows each desktop’s shortcut slot on its header, and not on its rows', async () => {
      await launch({
        sessions: [daemonSession('a1'), daemonSession('a2'), daemonSession('b1')],
        desktops: [
          splitDesktop('a', ['a1', 'a2'], { order_key: '1', shortcut_slot: 1 }),
          splitDesktop('b', ['b1'], { order_key: '2', shortcut_slot: 2 }),
        ],
      });

      expect(within(desktopGroup('a')).getByText('⌘1')).toBeInTheDocument();
      expect(within(desktopGroup('b')).getByText('⌘2')).toBeInTheDocument();
      expect(screen.getByTestId('sidebar-session-a1')).not.toHaveTextContent('⌘1');
    });

    it('lists a tile-only desktop without a session state', async () => {
      const tileOnly = daemonDesktop('docs', { root: { type: 'tile', tile_id: 'tile-notes', tile_kind: 'markdown', tile_params: '/repo/docs/notes.md' } }, { name: 'docs' });
      await launch({ sessions: [daemonSession('a1')], desktops: [soloDesktop('a1'), tileOnly] });

      expect(within(desktopGroup('docs')).getByTestId('desktop-neutral-indicator')).toBeInTheDocument();
      expect(desktopGroup('docs').querySelector('.state-indicator')).toBeNull();
      expect(within(desktopGroup('desktop-a1')).queryByTestId('desktop-neutral-indicator')).toBeNull();
    });

    it('lists a desktop’s browser tile after its session, and opens, reloads and closes it', async () => {
      const desktop = desktopWithTiles([{ tile_id: 'tile-browser', tile_kind: 'browser', tile_params: 'https://www.example.test' }], { id: 'ws' });
      vi.mocked(isTauri).mockReturnValue(true);
      vi.mocked(invoke).mockResolvedValue(undefined);
      const { daemon } = await launch({ sessions: [daemonSession('s1')], desktops: [desktop] });
      const tile = screen.getByTestId('sidebar-tile-ws-tile-browser');
      expect(tile).toHaveTextContent('www.example.test');
      expect(screen.getByTestId('sidebar-session-s1').compareDocumentPosition(tile) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

      await gesture(daemon, () => fireEvent.click(within(tile).getByRole('button', { name: 'Open www.example.test' })));
      expect(daemon.sentOf('desktop_set_current').slice(-1)).toEqual([expect.objectContaining({ desktop_id: 'ws' })]);

      await gesture(daemon, () => fireEvent.click(within(tile).getByRole('button', { name: 'Reload www.example.test' })));
      expect(vi.mocked(invoke)).toHaveBeenCalledWith('browser_host_control', expect.objectContaining({ label: 'browser-ws-tile-browser', action: 'reload' }));

      await gesture(daemon, () => fireEvent.click(within(tile).getByRole('button', { name: 'Close www.example.test' })));
      expect(daemon.sentOf('desktop_remove_leaf')).toEqual([expect.objectContaining({ desktop_id: 'ws', leaf_id: 'tile-browser' })]);
    });
  });

  describe('dragging', () => {
    async function twoDesktops() {
      const view = await launch({
        sessions: [daemonSession('s1'), daemonSession('s2'), daemonSession('t1')],
        desktops: [
          splitDesktop('source', ['s1', 's2'], { order_key: '0' }),
          splitDesktop('target', ['t1'], { order_key: '1' }),
        ],
      });
      view.daemon.on('desktop_move_leaf', (command) => ({ event: 'profile_action_result', action: command.cmd, request_id: command.request_id, success: true }));
      return view;
    }

    function pressRow(sessionId: string) {
      const select = screen.getByTestId(`sidebar-session-${sessionId}`).querySelector('.sidebar-row-select')!;
      fireEvent.pointerDown(select, { button: 0, pointerId: 1, clientX: 10, clientY: 10 });
      return select;
    }

    it('drags a session row onto another desktop, which the source desktop refuses', async () => {
      const { daemon } = await twoDesktops();

      pressRow('s2');
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 40 });
      expect(screen.getByTestId('session-drag-ghost')).toHaveTextContent('s2');
      fireEvent.pointerEnter(desktopGroup('source'));
      fireEvent.pointerUp(desktopGroup('source'), { pointerId: 1 });
      await daemon.idle();
      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);

      pressRow('s2');
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 10, clientY: 40 });
      fireEvent.pointerEnter(desktopGroup('target'));
      fireEvent.pointerUp(desktopGroup('target'), { pointerId: 1 });
      await daemon.idle();

      expect(daemon.sentOf('desktop_move_leaf')).toEqual([
        expect.objectContaining({ source_desktop_id: 'source', leaf_id: 'pane-s2', target_desktop_id: 'target' }),
      ]);
    });

    it('treats a press that barely moves as a click on the row', async () => {
      const { daemon } = await twoDesktops();

      const select = pressRow('s2');
      fireEvent.pointerMove(window, { pointerId: 1, clientX: 11, clientY: 12 });
      fireEvent.pointerUp(window, { pointerId: 1, clientX: 11, clientY: 12 });
      await gesture(daemon, () => fireEvent.click(select));

      expect(screen.queryByTestId('session-drag-ghost')).toBeNull();
      expect(daemon.sentOf('desktop_move_leaf')).toEqual([]);
      expect(shownPane()).toBe('pane-s2');
    });
  });

  describe('Sidebar settings', () => {
    it('turns the agent queue on, which swaps the desktop tree for the queue sidebar', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('s1')] });

      const settings = await openSidebarSettings(daemon);
      const toggle = settings.getByRole('switch', { name: /Agent queue/i });
      expect(toggle).toHaveAttribute('aria-checked', 'false');
      await gesture(daemon, () => fireEvent.click(toggle));

      expect(savedSetting(daemon, 'queue_mode_enabled')).toEqual(['true']);
      expect(screen.getByTestId('queue-sidebar')).toBeInTheDocument();
    });

    it.each([
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

      const second = await restartApp(first, { initialState: { ...options, desktops: [soloDesktop('s1')] } });
      const reopened = await openSidebarSettings(second.daemon);
      expect(reopened.getByRole('button', { name: 'dim' })).toHaveAttribute('aria-pressed', 'true');
    });

    it('hands focus to the control the user clicks to dismiss it', async () => {
      const { daemon } = await launch({ sessions: [daemonSession('s1')] });
      await openSidebarSettings(daemon);
      const other = screen.getByRole('button', { name: 'Actions for s1' });

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

    it('keeps a way home when the sidebar is collapsed, and a waiting desktop still shows its badge', async () => {
      const { daemon } = await launch({
        sessions: [daemonSession('s1', { state: 'idle' }), daemonSession('s2', { state: 'waiting_input' })],
        desktops: [soloDesktop('s1', { name: 's1' }), soloDesktop('s2', { name: 's2' })],
      });
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

