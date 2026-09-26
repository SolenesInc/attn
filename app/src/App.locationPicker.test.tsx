import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { CommandMessage, EventMessage } from './test/protocol';
import { openSessionsLedger, page, pages, rows } from './components/ledger/testSupport';
import { agentWorkspace, daemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';
import { closedEntry, verdict } from './test/sessionLedgerFixtures';
import { serveSettings } from './test/settings';
import { SessionReopenAction } from './types/generated';
import {
  HOME,
  inspection,
  chosenRow,
  destinationMemory,
  launchedAt,
  openPicker,
  pathInput,
  press,
  remoteEndpoint,
  repoInfo,
  serveLaunches,
  serveMachine,
  submitPath,
  typePath,
} from './test/locations';

type InitialState = Partial<EventMessage<'initial_state'>>;
type HeldCommand = 'inspect_path' | 'get_repo_info' | 'create_worktree' | 'get_recent_locations';

const REPO = `${HOME}/projects/exsin`;
const FEAT_IMAGES = `${REPO}--feat-images`;
const EXSIN = repoInfo(REPO, [{ path: FEAT_IMAGES, branch: 'feat-images' }]);
const GPU_BOX = remoteEndpoint('ep-1', 'gpu-box', { agents_available: ['codex'], projects_directory: '/srv/projects' });

function row(index: number) {
  return screen.getByTestId(`location-picker-item-${index}`);
}

function highlighted() {
  return screen.queryAllByRole('option', { selected: true });
}

function pickerOpen() {
  return screen.queryByTestId('location-picker') !== null;
}

function radio(name: RegExp) {
  return screen.getByRole('radio', { name });
}

function holdAnswers<C extends HeldCommand>(daemon: ScriptedDaemon, cmd: C) {
  daemon.on(cmd, () => undefined);
  return (index = 0) => daemon.sentOf(cmd)[index] as CommandMessage<C>;
}

async function answer(daemon: ScriptedDaemon, command: CommandMessage<HeldCommand>, reply: Reply) {
  daemon.replyTo(command, { ...reply, request_id: 'request_id' in command ? command.request_id : undefined } as Reply);
  await daemon.idle();
}

describe('App location picker', () => {
  describe('choosing a location', () => {
    it('launches the suggestion the user clicks', async () => {
      const { daemon } = await openPicker({ directories: { [HOME]: ['projects', 'project-archive'] } });

      await typePath(daemon, '~/pro');
      await gesture(daemon, () => fireEvent.click(row(0)));

      expect(launchedAt(daemon)).toEqual([{ cwd: `${HOME}/projects`, agent: 'claude' }]);
    });

    it('completes the ghost suggestion with Tab, or the highlighted row once the arrows moved to it', async () => {
      const { daemon } = await openPicker({ directories: { [HOME]: ['projects', 'project-archive'] } });

      await typePath(daemon, '~/pro');
      expect(screen.getByText('jects')).toBeInTheDocument();
      await press(daemon, 'Tab');
      expect(pathInput()).toHaveValue('~/projects');

      await typePath(daemon, '~/pro');
      await press(daemon, 'ArrowDown');
      await press(daemon, 'ArrowDown');
      expect(pathInput()).toHaveValue('~/pro');
      await press(daemon, 'Tab');
      expect(pathInput()).toHaveValue('~/project-archive');
      expect(highlighted()).toEqual([]);
    });

    it('lets the first Escape drop a highlight the arrows made and the next close the picker, while a window ArrowDown moves nothing', async () => {
      const { daemon } = await openPicker({ directories: { [HOME]: ['projects', 'project-archive'] } });
      await typePath(daemon, '~/pro');

      fireEvent.keyDown(window, { key: 'ArrowDown' });
      await daemon.idle();
      expect(pathInput()).toHaveValue('~/pro');
      expect(highlighted()).toEqual([]);

      await press(daemon, 'ArrowDown');
      expect(highlighted()).toEqual([row(0)]);
      await press(daemon, 'Escape');
      expect(pickerOpen()).toBe(true);
      expect(highlighted()).toEqual([]);

      await press(daemon, 'Escape');
      expect(pickerOpen()).toBe(false);
    });

    it.each([
      ['on this machine', {}, undefined],
      ['on a remote endpoint', { endpoints: [GPU_BOX] }, 'ep-1'],
    ])('launches at the root directory as typed %s', async (_, initialState: InitialState, endpointId) => {
      const { daemon } = await openPicker({}, initialState);
      if (endpointId) await gesture(daemon, () => fireEvent.click(radio(/gpu-box/i)));

      await submitPath(daemon, '/');

      expect(daemon.sentOf('inspect_path').map(({ path, endpoint_id }) => ({ path, endpoint_id }))).toEqual([{ path: '/', endpoint_id: endpointId }]);
      expect(launchedAt(daemon)).toEqual([{ cwd: '/', agent: endpointId ? 'codex' : 'claude', ...(endpointId ? { endpoint_id: endpointId } : {}) }]);
    });
  });

  describe('recent locations', () => {
    const RECENT = `${HOME}/projects/recent-repo`;

    it('names each recent location by its folder, and opens the top one on a bare Enter', async () => {
      const { daemon } = await openPicker({ recent: [RECENT, '/tmp/other'] });

      expect(within(row(0)).getByText('recent-repo')).toBeInTheDocument();
      expect(within(row(0)).getByText('~/projects/recent-repo')).toBeInTheDocument();
      expect(highlighted()).toEqual([row(0)]);

      await press(daemon, 'Enter');

      expect(daemon.sentOf('inspect_path').map(({ path }) => path)).toEqual([RECENT]);
      expect(launchedAt(daemon)).toEqual([{ cwd: RECENT, agent: 'claude' }]);
    });

    it('closes on Escape while the highlight is the automatic one', async () => {
      const { daemon } = await openPicker({ recent: [RECENT] });

      await press(daemon, 'Escape');

      expect(pickerOpen()).toBe(false);
    });

    it('makes the highlight the user’s own once the arrows move it, so Escape drops it first and Enter opens it', async () => {
      const { daemon } = await openPicker({ recent: [RECENT, '/tmp/other'] });

      await press(daemon, 'ArrowDown');
      await press(daemon, 'Escape');
      expect(pickerOpen()).toBe(true);
      expect(highlighted()).toEqual([]);

      await press(daemon, 'ArrowDown');
      await press(daemon, 'ArrowDown');
      await press(daemon, 'Enter');
      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/other', agent: 'claude' }]);
    });

    it('opens the typed path rather than the automatic highlight once the user types', async () => {
      const { daemon } = await openPicker({ recent: [RECENT] });

      await submitPath(daemon, '/tmp/typed');

      expect(daemon.sentOf('inspect_path').map(({ path }) => path)).toEqual(['/tmp/typed']);
      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/typed', agent: 'claude' }]);
    });

    it('does not highlight the recents it remembers on reopen until the daemon answers again', async () => {
      const { daemon } = await openPicker({ recent: [RECENT] });
      await press(daemon, 'Escape');
      const recentRequest = holdAnswers(daemon, 'get_recent_locations');

      await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      expect(row(0)).toBeInTheDocument();
      expect(highlighted()).toEqual([]);
      await press(daemon, 'Enter');
      expect(launchedAt(daemon)).toEqual([]);

      await answer(daemon, recentRequest(1), {
        event: 'recent_locations_result',
        success: true,
        home_path: HOME,
        recent_locations: [{ path: RECENT, last_seen: '2026-05-16T16:00:00Z', use_count: 1 }],
      });
      expect(highlighted()).toEqual([row(0)]);
    });
  });

  describe('agents and targets', () => {
    const agentChosen = (name: RegExp) => radio(name).getAttribute('aria-checked') === 'true';

    it('launches on a remote endpoint with an agent only that endpoint offers, without reading repo info', async () => {
      const { daemon } = await openPicker({}, {
        endpoints: [remoteEndpoint('ep-1', 'gpu-box', { agents_available: ['snipe'], projects_directory: '/srv/projects' })],
      });

      await gesture(daemon, () => fireEvent.click(radio(/gpu-box/i)));
      expect(agentChosen(/snipe/i)).toBe(true);
      await submitPath(daemon, '~/projects/remote-repo');

      expect(daemon.sentOf('inspect_path').map(({ path, endpoint_id }) => ({ path, endpoint_id }))).toEqual([{ path: `${HOME}/projects/remote-repo`, endpoint_id: 'ep-1' }]);
      expect(daemon.sentOf('get_repo_info')).toEqual([]);
      expect(launchedAt(daemon)).toEqual([{ cwd: `${HOME}/projects/remote-repo`, agent: 'snipe', endpoint_id: 'ep-1' }]);
    });

    it('offers Terminal on a remote endpoint that reports only agent CLIs', async () => {
      const { daemon } = await openPicker({}, { endpoints: [GPU_BOX] });

      await gesture(daemon, () => fireEvent.click(radio(/gpu-box/i)));

      expect(radio(/terminal/i)).toBeEnabled();
      expect(radio(/codex/i)).toBeEnabled();
    });

    it('switches agents with ⌥ and a digit, and neither advertises nor applies one for an unavailable agent', async () => {
      const { daemon } = await openPicker({}, { settings: { codex_available: 'false', copilot_available: 'true' } });
      expect(within(screen.getByRole('radiogroup', { name: 'Initial workspace session agent' })).queryByText('⌥2')).toBeNull();

      await press(daemon, '™', { code: 'Digit2', altKey: true });

      expect(agentChosen(/claude/i)).toBe(true);
      expect(agentChosen(/copilot/i)).toBe(false);
    });

    it.each([
      ['workspace', 'session.newWorkspace' as const],
      ['session', 'session.new' as const],
    ])('offers Terminal on ⌥T in the %s picker and launches a shell without remembering it as the preferred agent', async (_, shortcut) => {
      const view = await renderApp({ initialState: { sessions: [daemonSession('s1', { state: 'idle' })], workspaces: [agentWorkspace('s1')] } });
      const { daemon } = view;
      serveMachine(daemon);
      serveLaunches(daemon);
      serveSettings(daemon);
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
      await gesture(daemon, () => pressShortcut(shortcut));
      expect(within(radio(/terminal/i)).getByText('⌥T')).toBeInTheDocument();

      await press(daemon, '†', { code: 'KeyT', altKey: true });
      expect(agentChosen(/terminal/i)).toBe(true);
      await submitPath(daemon, '/tmp/shell-here');

      expect(launchedAt(daemon).map(({ agent, cwd }) => ({ agent, cwd }))).toEqual([{ agent: 'shell', cwd: '/tmp/shell-here' }]);
      expect(daemon.sentOf('set_setting').filter(({ key }) => key === 'new_session_agent')).toEqual([]);
    });

    it('switches targets with ⌥ shortcuts in the order they are shown, starting each from its projects directory, and skips a disconnected one', async () => {
      const { daemon } = await openPicker({}, {
        settings: { projects_directory: '/Users/victor/projects' },
        endpoints: [
          GPU_BOX,
          remoteEndpoint('ep-2', 'lab-box', { projects_directory: '/opt/work' }),
          remoteEndpoint('ep-3', 'dark-box', {}, 'disconnected'),
        ],
      });
      expect(pathInput()).toHaveValue('/Users/victor/projects/');
      expect(within(radio(/dark-box/i)).queryByText(/⌥/)).toBeNull();

      await press(daemon, '∑', { code: 'KeyW', altKey: true });
      expect(pathInput()).toHaveValue('/srv/projects/');
      expect(radio(/gpu-box/i)).toHaveAttribute('aria-checked', 'true');

      await press(daemon, '€', { code: 'KeyE', altKey: true });
      expect(pathInput()).toHaveValue('/opt/work/');
      expect(radio(/lab-box/i)).toHaveAttribute('aria-checked', 'true');

      await press(daemon, '®', { code: 'KeyR', altKey: true });
      expect(radio(/lab-box/i)).toHaveAttribute('aria-checked', 'true');

      await press(daemon, 'œ', { code: 'KeyQ', altKey: true });
      expect(pathInput()).toHaveValue('/Users/victor/projects/');
      expect(radio(/local/i)).toHaveAttribute('aria-checked', 'true');
    });

    it('turns on yolo for a remote daemon when its target is chosen again, remembers that for the daemon, and launches with it', async () => {
      const { daemon } = await openPicker({}, {
        settings: { claude_cap_yolo: 'true' },
        endpoints: [remoteEndpoint('ep-1', 'gpu-box', { protocol_version: '47', daemon_instance_id: 'daemon-remote-1', agents_available: ['claude'] })],
      });

      await gesture(daemon, () => fireEvent.click(radio(/gpu-box/i)));
      await gesture(daemon, () => fireEvent.click(radio(/gpu-box/i)));
      await submitPath(daemon, '~/projects/remote-repo');

      expect(daemon.sentOf('set_setting')).toEqual([{ cmd: 'set_setting', key: 'new_session_yolo_daemon_daemon-remote-1', value: 'true' }]);
      expect(launchedAt(daemon)).toEqual([{ cwd: `${HOME}/projects/remote-repo`, agent: 'claude', endpoint_id: 'ep-1', yolo_mode: true }]);
    });
  });

  describe('launching as chief of staff', () => {
    const chiefToggle = () => screen.queryByTestId('location-picker-chief-toggle');

    it('offers the chief toggle only for a claude workspace while no chief exists', async () => {
      const { daemon } = await openPicker();
      expect(chiefToggle()).not.toBeNull();
      await gesture(daemon, () => fireEvent.click(radio(/terminal/i)));
      expect(chiefToggle()).toBeNull();

      await openPicker({}, { sessions: [daemonSession('chief', { chief_of_staff: true })], workspaces: [agentWorkspace('chief')] });
      expect(chiefToggle()).toBeNull();
    });

    it('leaves the chief toggle out of the picker for a session beside the current one', async () => {
      const view = await renderApp({ initialState: { sessions: [daemonSession('s1', { state: 'idle' })], workspaces: [agentWorkspace('s1')] } });
      serveMachine(view.daemon);
      await gesture(view.daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
      await gesture(view.daemon, () => pressShortcut('session.new'));

      expect(screen.getByTestId('location-picker-title')).toHaveTextContent('New Session Location');
      expect(chiefToggle()).toBeNull();
    });

    it('launches as chief only when the toggle is on', async () => {
      const { daemon } = await openPicker();
      await submitPath(daemon, '/tmp/plain');
      await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      await gesture(daemon, () => fireEvent.click(chiefToggle()!));
      await submitPath(daemon, '/tmp/chief');

      expect(launchedAt(daemon)).toEqual([
        { cwd: '/tmp/plain', agent: 'claude' },
        { cwd: '/tmp/chief', agent: 'claude', chief_of_staff: true },
      ]);
    });
  });

  describe('auto mode', () => {
    const AUTO_MODE_AGENT = { snipe_available: 'true', snipe_cap_auto_mode: 'true', claude_cap_auto_mode: 'false' };
    const autoMode = () => screen.queryByTestId('location-picker-automode-toggle');

    async function openWithSnipe(settings: Record<string, string> = AUTO_MODE_AGENT) {
      const view = await openPicker({}, { settings });
      await gesture(view.daemon, () => fireEvent.click(radio(/snipe/i)));
      return view;
    }

    async function defaultBecomes(daemon: ScriptedDaemon, value: string) {
      daemon.emit({ event: 'settings_updated', settings: { ...AUTO_MODE_AGENT, automode_enabled_default: value } });
      await daemon.idle();
    }

    it('offers the toggle only for an agent whose driver advertises auto mode, and a launch without it says nothing about auto mode', async () => {
      const { daemon } = await openPicker({}, { settings: AUTO_MODE_AGENT });
      expect(autoMode()).toBeNull();

      await submitPath(daemon, '/tmp/claude-here');

      expect(launchedAt(daemon)[0]).not.toHaveProperty('auto_mode');
      await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      await gesture(daemon, () => fireEvent.click(radio(/snipe/i)));
      expect(autoMode()).not.toBeNull();
    });

    it('starts from the default setting and launches with it when left alone', async () => {
      const { daemon } = await openWithSnipe({ ...AUTO_MODE_AGENT, automode_enabled_default: 'false' });
      expect(autoMode()).toHaveAttribute('aria-checked', 'false');

      await defaultBecomes(daemon, 'true');
      expect(autoMode()).toHaveAttribute('aria-checked', 'true');

      await submitPath(daemon, '/tmp/snipe-here');
      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/snipe-here', agent: 'snipe', auto_mode: true }]);
    });

    it('sends an explicit off once the user turns it off, whatever the default does after', async () => {
      const { daemon } = await openWithSnipe();
      expect(autoMode()).toHaveAttribute('aria-checked', 'true');

      await gesture(daemon, () => fireEvent.click(autoMode()!));
      await defaultBecomes(daemon, 'false');
      await defaultBecomes(daemon, 'true');
      expect(autoMode()).toHaveAttribute('aria-checked', 'false');

      await submitPath(daemon, '/tmp/snipe-here');
      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/snipe-here', agent: 'snipe', auto_mode: false }]);
    });
  });

  describe('in a repository', () => {
    const MEMORY = destinationMemory(REPO);
    const chooser = () => screen.queryByTestId('repo-options');
    const memoryWrites = (daemon: ScriptedDaemon) => daemon.sentOf('set_setting').filter(({ key }) => key.startsWith('new_session_destination_'));

    async function openRepo(settings: Record<string, string> = {}, path = '~/projects/exsin', initialState: InitialState = {}) {
      const view = await openPicker({ repos: [EXSIN] }, { settings, ...initialState });
      if (initialState.endpoints) await gesture(view.daemon, () => fireEvent.click(radio(/gpu-box/i)));
      await submitPath(view.daemon, path);
      return view;
    }

    it('preselects the worktree a remote path names, asking that endpoint for the repository', async () => {
      const { daemon } = await openRepo({}, '~/projects/exsin--feat-images/', { endpoints: [GPU_BOX] });

      expect(daemon.sentOf('inspect_path').map(({ path, endpoint_id }) => ({ path, endpoint_id }))).toEqual([{ path: FEAT_IMAGES, endpoint_id: 'ep-1' }]);
      expect(daemon.sentOf('get_repo_info').map(({ repo, endpoint_id }) => ({ repo, endpoint_id }))).toEqual([{ repo: REPO, endpoint_id: 'ep-1' }]);
      expect(chosenRow()).toBe(1);
    });

    it('keeps the dialog’s ⌥ shortcuts while the chooser’s rows have the keyboard', async () => {
      const { daemon } = await openRepo({}, '~/projects/exsin--feat-images');

      await press(daemon, '™', { code: 'Digit2', altKey: true }, chooser()!);

      expect(radio(/codex/i)).toHaveAttribute('aria-checked', 'true');
    });

    it('keeps the chosen worktree when the repository is refreshed', async () => {
      const { daemon } = await openRepo({}, '~/projects/exsin--feat-images');

      await press(daemon, 'r', {}, chooser()!);

      expect(daemon.sentOf('get_repo_info')).toHaveLength(2);
      expect(chosenRow()).toBe(1);
    });

    it('remembers that a repository was started in its main checkout, and opens on that row next time', async () => {
      const { daemon } = await openRepo();

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('repo-option-0')));
      expect(memoryWrites(daemon)).toEqual([{ cmd: 'set_setting', key: MEMORY, value: 'main_repo' }]);
      expect(launchedAt(daemon)).toEqual([{ cwd: REPO, agent: 'claude' }]);

      await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      await submitPath(daemon, '~/projects/exsin');
      expect(chosenRow()).toBe(0);
      await press(daemon, 'Enter', {}, chooser()!);

      expect(memoryWrites(daemon)).toHaveLength(1);
      expect(daemon.sentOf('create_worktree')).toEqual([]);
      expect(launchedAt(daemon)).toEqual([{ cwd: REPO, agent: 'claude' }, { cwd: REPO, agent: 'claude' }]);
    });

    it('remembers a new worktree when one is created, and leaves the memory alone when an existing worktree is opened', async () => {
      const { daemon } = await openRepo({ [MEMORY]: 'main_repo' });

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('repo-option-1')));
      expect(launchedAt(daemon)).toEqual([{ cwd: FEAT_IMAGES, agent: 'claude' }]);
      expect(memoryWrites(daemon)).toEqual([]);

      await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      await submitPath(daemon, '~/projects/exsin');
      await press(daemon, 'ArrowUp', {}, chooser()!);
      fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feat-more' } });
      await press(daemon, 'Enter', {}, chooser()!);

      expect(memoryWrites(daemon)).toEqual([{ cmd: 'set_setting', key: MEMORY, value: 'new_worktree' }]);
    });

    it('keeps each endpoint’s memory apart, since the same path on a remote is another repository', async () => {
      const { daemon } = await openRepo({ [MEMORY]: 'main_repo' }, '~/projects/exsin', { endpoints: [GPU_BOX] });
      expect(screen.getByTestId('repo-new-worktree-input')).toHaveFocus();

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('repo-option-0')));

      expect(memoryWrites(daemon)).toEqual([{ cmd: 'set_setting', key: destinationMemory(REPO, 'ep-1'), value: 'main_repo' }]);
    });

    it('shows what it is waiting for while the path and then the repository are read', async () => {
      const { daemon } = await openPicker({ repos: [EXSIN] });
      const inspectRequest = holdAnswers(daemon, 'inspect_path');
      const repoRequest = holdAnswers(daemon, 'get_repo_info');

      await submitPath(daemon, '~/projects/exsin');
      expect(screen.getByRole('status', { name: 'Inspecting path' })).toBeInTheDocument();

      await answer(daemon, inspectRequest(), inspection('~/projects/exsin', { repoRoot: REPO }));
      expect(screen.queryByRole('status', { name: 'Inspecting path' })).toBeNull();
      expect(screen.getByRole('status', { name: 'Loading repo options' })).toBeInTheDocument();

      await answer(daemon, repoRequest(), { event: 'get_repo_info_result', success: true, info: EXSIN });
      expect(chooser()).not.toBeNull();
    });

    it('says a missing directory is missing, and stops showing progress', async () => {
      const { daemon } = await openPicker();
      daemon.on('inspect_path', ({ path }) => inspection(path, { exists: false }));

      await submitPath(daemon, '~/missing');

      expect(screen.getByRole('alert')).toHaveTextContent(`Directory not found: ${HOME}/missing`);
      expect(screen.queryByRole('status', { name: 'Inspecting path' })).toBeNull();
      expect(launchedAt(daemon)).toEqual([]);
    });
  });

  describe('answers that arrive too late', () => {
    it.each([
      ['the user picked another target', async (daemon: ScriptedDaemon) => gesture(daemon, () => fireEvent.click(radio(/gpu-box/i)))],
      ['the user typed another path', async (daemon: ScriptedDaemon) => typePath(daemon, '/tmp/other')],
      ['the picker was closed and opened again', async (daemon: ScriptedDaemon) => {
        await gesture(daemon, () => fireEvent.click(screen.getByTestId('location-picker-overlay')));
        await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      }],
    ])('ignores a path inspection answered after %s, success or failure', async (_, moveOn) => {
      const { daemon } = await openPicker({ repos: [EXSIN] }, { endpoints: [GPU_BOX] });
      const inspectRequest = holdAnswers(daemon, 'inspect_path');
      await submitPath(daemon, '~/projects/exsin');
      await submitPath(daemon, '~/projects/exsin');

      await moveOn(daemon);
      await answer(daemon, inspectRequest(0), inspection('~/projects/exsin', { repoRoot: REPO }));
      await answer(daemon, inspectRequest(1), { event: 'inspect_path_result', success: false, error: 'stale failure' });

      expect(daemon.sentOf('get_repo_info')).toEqual([]);
      expect(screen.queryByTestId('repo-options')).toBeNull();
      expect(screen.queryByRole('alert')).toBeNull();
      expect(launchedAt(daemon)).toEqual([]);
    });

    it('ignores repository options answered after the picker was closed and opened again', async () => {
      const { daemon } = await openPicker({ repos: [EXSIN] });
      const repoRequest = holdAnswers(daemon, 'get_repo_info');
      await submitPath(daemon, '~/projects/exsin');

      await press(daemon, 'Escape');
      await gesture(daemon, () => pressShortcut('session.newWorkspace'));
      await answer(daemon, repoRequest(), { event: 'get_repo_info_result', success: true, info: EXSIN });

      expect(screen.getByTestId('location-picker-title')).toBeInTheDocument();
      expect(screen.queryByTestId('repo-options')).toBeNull();
    });

    it('ignores repository options answered after a newer submission already launched elsewhere', async () => {
      const { daemon } = await openPicker({ repos: [EXSIN] });
      const repoRequest = holdAnswers(daemon, 'get_repo_info');
      await submitPath(daemon, '~/projects/exsin');

      await submitPath(daemon, '/tmp/other');
      await answer(daemon, repoRequest(), { event: 'get_repo_info_result', success: true, info: EXSIN });

      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/other', agent: 'claude' }]);
      expect(screen.queryByTestId('repo-options')).toBeNull();
    });
  });

  describe('choosing where to start a closed session afresh', () => {
    const PROJECTS = '/home/u/projects';
    const FRESH_REPO = '/home/u/repo';

    async function openReopenPicker() {
      const view = await openSessionsLedger(pages([page({ entries: [closedEntry('s1')] })]), {
        initialState: { settings: { projects_directory: PROJECTS } },
      });
      const { daemon } = view;
      serveMachine(daemon, { repos: [repoInfo(FRESH_REPO)] });
      serveLaunches(daemon);
      daemon.on('session_reopen', ({ session_id, action, directory }) => (
        action === SessionReopenAction.StartFreshElsewhere
          ? { event: 'session_reopen_result', success: true, result: { action, directory: directory!, session_id, workspace_id: 'ws-1' } }
          : {
            event: 'session_reopen_result',
            success: false,
            error: `${session_id} cannot be reopened with reopen: the directory is gone. Offered instead: start_fresh_elsewhere`,
            reopen: verdict({ reopenable: false, reason: 'the directory is gone', directory_state: 'missing', actions: [SessionReopenAction.StartFreshElsewhere] }),
          }
      ));
      const closedRow = () => rows().getByText('run s1').closest<HTMLElement>('.ledger-row')!;
      await gesture(daemon, () => fireEvent.click(within(closedRow()).getByRole('button', { name: 'Reopen' })));
      await gesture(daemon, () => fireEvent.click(within(closedRow()).getByRole('button', { name: 'Start fresh elsewhere' })));
      return view;
    }

    const freshStarts = (daemon: ScriptedDaemon) => daemon.sentOf('session_reopen')
      .filter(({ action }) => action === SessionReopenAction.StartFreshElsewhere)
      .map(({ directory }) => directory);

    async function createWorktree(daemon: ScriptedDaemon, name: string) {
      await submitPath(daemon, FRESH_REPO);
      await press(daemon, 'ArrowUp', {}, screen.getByTestId('repo-options'));
      fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: name } });
      await press(daemon, 'Enter', {}, screen.getByTestId('repo-options'));
    }

    it('asks only for a path, starting from the projects directory with the caret at its end, and starts there', async () => {
      const { daemon } = await openReopenPicker();

      expect(screen.getByTestId('location-picker-title')).toHaveTextContent('Start fresh where?');
      expect(screen.queryByRole('radiogroup', { name: 'Session agent' })).toBeNull();
      expect(screen.queryByRole('radiogroup', { name: 'Session target' })).toBeNull();
      expect(pathInput()).toHaveValue(`${PROJECTS}/`);
      expect(pathInput()).toHaveFocus();
      expect(pathInput().selectionStart).toBe(`${PROJECTS}/`.length);

      await submitPath(daemon, '/home/u/wt/fresh');

      expect(freshStarts(daemon)).toEqual(['/home/u/wt/fresh']);
      expect(pickerOpen()).toBe(false);
    });

    it('creates a new worktree itself from the default branch and starts there, without launching a session of its own', async () => {
      const { daemon } = await openReopenPicker();

      await createWorktree(daemon, 'fresh');

      expect(daemon.sentOf('create_worktree')).toEqual([{ cmd: 'create_worktree', main_repo: FRESH_REPO, branch: 'fresh', starting_from: 'origin/main' }]);
      expect(freshStarts(daemon)).toEqual([`${FRESH_REPO}--fresh`]);
      expect(launchedAt(daemon)).toEqual([]);
    });

    it('shows the worktree being created in its form, and ignores another Enter meanwhile', async () => {
      const { daemon } = await openReopenPicker();
      const createRequest = holdAnswers(daemon, 'create_worktree');

      await createWorktree(daemon, 'fresh');
      expect(screen.getByText('Creating worktree...')).toBeInTheDocument();
      expect(screen.getByTestId('repo-new-worktree-input')).toBeDisabled();
      await press(daemon, 'Enter', {}, screen.getByTestId('repo-options'));
      expect(daemon.sentOf('create_worktree')).toHaveLength(1);

      await answer(daemon, createRequest(), { event: 'create_worktree_result', success: true, path: `${FRESH_REPO}--fresh` });
      expect(freshStarts(daemon)).toEqual([`${FRESH_REPO}--fresh`]);
    });

    it('starts nothing from a worktree created after the picker closed', async () => {
      const { daemon } = await openReopenPicker();
      const createRequest = holdAnswers(daemon, 'create_worktree');
      await createWorktree(daemon, 'fresh');

      await gesture(daemon, () => fireEvent.click(screen.getByTestId('location-picker-overlay')));
      await answer(daemon, createRequest(), { event: 'create_worktree_result', success: true, path: `${FRESH_REPO}--fresh` });

      expect(pickerOpen()).toBe(false);
      expect(freshStarts(daemon)).toEqual([]);
    });
  });
});
