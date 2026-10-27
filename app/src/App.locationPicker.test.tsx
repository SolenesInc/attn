import { harnesses } from './test/harnessCatalogs';
import { act, fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { CommandMessage } from './test/protocol';
import { soloDesktop, daemonSession } from './test/daemonFixtures';
import { gesture, pressShortcut, renderApp } from './test/renderApp';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';
import { serveSettings } from './test/settings';
import {
  HOME,
  inspection,
  chosenRow,
  destinationMemory,
  launchedAt,
  openPicker,
  pathInput,
  press,
  repoInfo,
  serveLaunches,
  serveMachine,
  submitPath,
  typePath,
} from './test/locations';

type HeldCommand = 'inspect_path' | 'get_repo_info' | 'create_worktree' | 'get_recent_locations' | 'browse_directory';

const REPO = `${HOME}/projects/exsin`;
const FEAT_IMAGES = `${REPO}--feat-images`;
const EXSIN = repoInfo(REPO, [{ path: FEAT_IMAGES, branch: 'feat-images' }]);

function row(index: number) {
  return screen.getByTestId(`location-picker-item-${index}`);
}

function highlighted() {
  return screen.queryAllByRole('option', { selected: true });
}

function pickerOpen() {
  return screen.queryByTestId('location-picker') !== null;
}

function suggestions() {
  return screen.queryAllByTestId(/^location-picker-item-\d+$/).map((item) => item.querySelector('.picker-name')?.textContent);
}

function browsed(daemon: ScriptedDaemon) {
  return daemon.sentOf('browse_directory').map(({ input_path }) => ({ input_path }));
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
      expect(daemon.sentOf('get_recent_locations')).toHaveLength(1);
      expect(daemon.sentOf('browse_directory').map(({ input_path }) => input_path)).toEqual(['~/pro']);
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

    it('launches at the root directory as typed', async () => {
      const { daemon } = await openPicker();

      await submitPath(daemon, '/');

      expect(daemon.sentOf('inspect_path').map(({ path }) => path)).toEqual(['/']);
      expect(launchedAt(daemon)).toEqual([{ cwd: '/', agent: 'claude' }]);
    });

    it.each([
      ['/tmp/project/', '/tmp/project'],
      ['/tmp/project', '/tmp/project'],
      ['~/', HOME],
    ])('inspects %s as %s', async (typed, path) => {
      const { daemon } = await openPicker();

      await submitPath(daemon, typed);

      expect(daemon.sentOf('inspect_path').map((command) => command.path)).toEqual([path]);
    });
  });

  describe('browsing', () => {
    it('browses the daemon’s directories', async () => {
      const { daemon } = await openPicker({ directories: { [`${HOME}/projects`]: ['local-repo'] } });
      await typePath(daemon, '~/projects/');

      expect(suggestions()).toEqual(['local-repo']);
      expect(browsed(daemon)).toEqual([{ input_path: '~/projects/' }]);
    });

    it('asks once for the path the user settles on, not for every keystroke', async () => {
      const { daemon } = await openPicker();
      await typePath(daemon, '~/projects/');

      for (const path of ['~/projects/a', '~/projects/at', '~/projects/att', '~/projects/attn']) {
        fireEvent.change(pathInput(), { target: { value: path } });
        await act(() => vi.advanceTimersByTimeAsync(50));
      }
      await act(() => vi.advanceTimersByTimeAsync(150));
      await daemon.idle();

      expect(browsed(daemon)).toEqual([{ input_path: '~/projects/' }, { input_path: '~/projects/attn' }]);
    });

    it('stops browsing once the picker closes', async () => {
      const { daemon } = await openPicker({ directories: { [`${HOME}/projects`]: ['attn'] } });
      await typePath(daemon, '~/projects/');
      expect(suggestions()).toEqual(['attn']);

      fireEvent.change(pathInput(), { target: { value: '~/projects/attn' } });
      await press(daemon, 'Escape');
      await act(() => vi.advanceTimersByTimeAsync(500));
      await daemon.idle();

      expect(pickerOpen()).toBe(false);
      expect(browsed(daemon)).toEqual([{ input_path: '~/projects/' }]);
    });
    it('says when nothing matches a typed path', async () => {
      const { daemon } = await openPicker({ directories: { [`${HOME}/projects`]: ['attn'] } });

      await typePath(daemon, '~/projects/zzz');

      expect(suggestions()).toEqual([]);
      expect(screen.getByTestId('location-picker-empty')).toHaveTextContent('No matches. Press Enter to use path directly.');
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

      await gesture(daemon, () => pressShortcut('session.new'));
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

  describe('agents and YOLO', () => {
    const agentChosen = (name: RegExp) => radio(name).getAttribute('aria-checked') === 'true';

    it('launches an interactive plugin absent from the initial-prompt harness catalog', async () => {
      const { daemon } = await openPicker({}, { settings: { interactive_available: 'true', interactive_cap_initial_prompt: 'false' } });
      const choices = within(screen.getByRole('radiogroup', { name: 'Session agent' })).getAllByRole('radio');
      expect(choices.map(choice => within(choice).getByText(/Terminal|Claude|Codex|Pi|Interactive/).textContent)).toEqual(['Terminal', 'Claude', 'Codex', 'Pi', 'Interactive']);
      await gesture(daemon, () => fireEvent.click(radio(/interactive/i)));
      await submitPath(daemon, '/tmp/interactive-plugin');
      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/interactive-plugin', agent: 'interactive' }]);
    });

    it('keeps agents selectable after a catalog error and preserves selection through Retry', async () => {
      const { daemon } = await renderApp({ initialState: { settings: { aaa_available: 'true' } }, script: daemon => {
        daemon.on('delegation_preferences_get', () => ({ event: 'delegation_preferences_result', success: false, error: 'Catalog unavailable', preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } }, harnesses: [], templates: [] }));
      } });
      serveMachine(daemon);
      serveLaunches(daemon);
      serveSettings(daemon, { aaa_available: 'true' });
      await gesture(daemon, () => pressShortcut('session.new'));
      expect(screen.getByRole('alert')).toHaveTextContent('Catalog unavailable');
      expect(radio(/codex/i)).toBeEnabled();
      await gesture(daemon, () => fireEvent.click(radio(/^Aaa/)));
      expect(agentChosen(/^Aaa/)).toBe(true);
      expect(within(screen.getByRole('radiogroup', { name: 'Session agent' })).queryByText('⌥1')).toBeNull();
      await press(daemon, '1', { code: 'Digit1', altKey: true });
      expect(agentChosen(/^Aaa/)).toBe(true);
      await submitPath(daemon, '/tmp/catalog-unavailable');
      let held: CommandMessage<'delegation_preferences_get'> | undefined;
      daemon.on('delegation_preferences_get', request => { held = request; });
      await gesture(daemon, () => pressShortcut('session.new'));
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Retry' })));
      expect(radio(/^Aaa/)).toBeEnabled();
      expect(agentChosen(/^Aaa/)).toBe(true);
      await gesture(daemon, () => daemon.replyTo(held!, { event: 'delegation_preferences_result', request_id: held!.request_id, success: true, preferences: { enabled: false, revision: 0, workflow_skill_enabled: false, roles: [], fallback: { selection: { harness: '', provider: '', model: '', effort: '' }, instructions: '' } }, harnesses: [harnesses[1], harnesses[0], harnesses[2]], templates: [] }));
      expect(agentChosen(/^Aaa/)).toBe(true);
      expect(daemon.sentOf('delegation_preferences_get')).toHaveLength(2);
      await press(daemon, '1', { code: 'Digit1', altKey: true });
      expect(agentChosen(/^Codex/)).toBe(true);
      await submitPath(daemon, '/tmp/catalog-recovered');
      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/catalog-unavailable', agent: 'aaa' }, { cwd: '/tmp/catalog-recovered', agent: 'codex' }]);
    });

    it('remembers the agent the user picks for the next time the picker opens', async () => {
      const { daemon } = await openPicker({}, { settings: { codex_available: 'true' } });

      await gesture(daemon, () => fireEvent.click(radio(/codex/i)));
      await press(daemon, 'Escape');
      expect(pickerOpen()).toBe(false);
      await gesture(daemon, () => pressShortcut('session.new'));

      expect(daemon.sentOf('set_setting').filter(({ key }) => key === 'new_session_agent')).toEqual([{ cmd: 'set_setting', key: 'new_session_agent', value: 'codex' }]);
      expect(agentChosen(/codex/i)).toBe(true);
    });

    it('switches agents with ⌥ and a digit, and neither advertises nor applies one for an unavailable agent', async () => {
      const { daemon } = await openPicker({}, { settings: { codex_available: 'false', pi_available: 'true' } });
      expect(within(screen.getByRole('radiogroup', { name: 'Session agent' })).queryByText('⌥2')).toBeNull();

      await press(daemon, '™', { code: 'Digit2', altKey: true });

      expect(agentChosen(/claude/i)).toBe(true);
      expect(agentChosen(/pi/i)).toBe(false);
    });

    it('offers Terminal on ⌥T and launches a shell without remembering it as the preferred agent', async () => {
      const view = await renderApp({ initialState: { sessions: [daemonSession('s1', { state: 'idle' })], desktops: [soloDesktop('s1')] } });
      const { daemon } = view;
      serveMachine(daemon);
      serveLaunches(daemon);
      serveSettings(daemon);
      await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Open s1' })));
      await gesture(daemon, () => pressShortcut('session.new'));
      expect(within(radio(/terminal/i)).getByText('⌥T')).toBeInTheDocument();

      await press(daemon, '†', { code: 'KeyT', altKey: true });
      expect(agentChosen(/terminal/i)).toBe(true);
      await submitPath(daemon, '/tmp/shell-here');

      expect(launchedAt(daemon).map(({ agent, cwd }) => ({ agent, cwd }))).toEqual([{ agent: 'shell', cwd: '/tmp/shell-here' }]);
      expect(daemon.sentOf('spawn_session')[0].label ?? '').toBe('');
      expect(daemon.sentOf('set_setting').filter(({ key }) => key === 'new_session_agent')).toEqual([]);
    });

    const yoloSwitch = () => screen.getByTestId('location-picker-yolo-toggle');
    const yoloWrites = (daemon: ScriptedDaemon) => daemon.sentOf('set_setting').filter(({ key }) => key.startsWith('new_session_yolo'));

    it('turns YOLO on with ⌥Q or a click, remembers it for the next picker, and launches with it', async () => {
      const { daemon } = await openPicker({}, { settings: { claude_cap_yolo: 'true' } });
      expect(yoloSwitch()).toHaveAttribute('aria-checked', 'false');
      expect(within(yoloSwitch()).getByText('⌥Q')).toBeInTheDocument();

      await press(daemon, 'œ', { code: 'KeyQ', altKey: true });
      expect(yoloSwitch()).toHaveAttribute('aria-checked', 'true');
      await submitPath(daemon, '/tmp/yolo');

      await gesture(daemon, () => pressShortcut('session.new'));
      expect(yoloSwitch()).toHaveAttribute('aria-checked', 'true');
      await gesture(daemon, () => fireEvent.click(yoloSwitch()));
      await submitPath(daemon, '/tmp/careful');

      expect(yoloWrites(daemon)).toEqual([
        { cmd: 'set_setting', key: 'new_session_yolo_local', value: 'true' },
        { cmd: 'set_setting', key: 'new_session_yolo_local', value: 'false' },
      ]);
      expect(launchedAt(daemon)).toEqual([
        { cwd: '/tmp/yolo', agent: 'claude', yolo_mode: true },
        { cwd: '/tmp/careful', agent: 'claude' },
      ]);
    });

    it('keeps YOLO off for an agent that does not support it, whatever was remembered', async () => {
      const { daemon } = await openPicker({}, { settings: { new_session_yolo_local: 'true' } });
      expect(yoloSwitch()).toBeDisabled();
      expect(yoloSwitch()).toHaveAttribute('aria-checked', 'false');

      await press(daemon, 'œ', { code: 'KeyQ', altKey: true });
      await submitPath(daemon, '/tmp/plain');

      expect(yoloWrites(daemon)).toEqual([]);
      expect(launchedAt(daemon)).toEqual([{ cwd: '/tmp/plain', agent: 'claude' }]);
    });
  });

  describe('auto mode', () => {
    const AUTO_MODE_AGENT = { snipe_available: 'true', snipe_cap_auto_mode: 'true', claude_cap_auto_mode: 'false' };
    const autoModeHarnesses = [...harnesses, { id: 'snipe', name: 'Snipe', available: true, model_pin: true, effort_pin: true, discovery: false }];
    const autoMode = () => screen.queryByTestId('location-picker-automode-toggle');

    async function openWithSnipe(settings: Record<string, string> = AUTO_MODE_AGENT) {
      const view = await openPicker({}, { settings }, autoModeHarnesses);
      await gesture(view.daemon, () => fireEvent.click(radio(/snipe/i)));
      return view;
    }

    async function defaultBecomes(daemon: ScriptedDaemon, value: string) {
      daemon.emit({ event: 'settings_updated', settings: { ...AUTO_MODE_AGENT, automode_enabled_default: value } });
      await daemon.idle();
    }

    it('offers the toggle only for an agent whose driver advertises auto mode, and a launch without it says nothing about auto mode', async () => {
      const { daemon } = await openPicker({}, { settings: AUTO_MODE_AGENT }, autoModeHarnesses);
      expect(autoMode()).toBeNull();

      await submitPath(daemon, '/tmp/claude-here');

      expect(launchedAt(daemon)[0]).not.toHaveProperty('auto_mode');
      await gesture(daemon, () => pressShortcut('session.new'));
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

    async function openRepo(settings: Record<string, string> = {}, path = '~/projects/exsin') {
      const view = await openPicker({ repos: [EXSIN] }, { settings });
      await submitPath(view.daemon, path);
      return view;
    }

    it('starts in the inspected folder when repository options cannot be read', async () => {
      const { daemon } = await openPicker({ repos: [EXSIN] });
      daemon.on('get_repo_info', () => ({
        event: 'get_repo_info_result', success: false, error: 'Git metadata unavailable',
      }));

      await submitPath(daemon, '~/projects/exsin');

      expect(daemon.sentOf('get_repo_info')).toEqual([
        { cmd: 'get_repo_info', repo: REPO, request_id: expect.any(String) },
      ]);
      expect(launchedAt(daemon)).toEqual([{ cwd: REPO, agent: 'claude' }]);
      expect(pickerOpen()).toBe(false);
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

      await gesture(daemon, () => pressShortcut('session.new'));
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

      await gesture(daemon, () => pressShortcut('session.new'));
      await submitPath(daemon, '~/projects/exsin');
      await press(daemon, 'ArrowUp', {}, chooser()!);
      fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feat-more' } });
      await press(daemon, 'Enter', {}, chooser()!);

      expect(memoryWrites(daemon)).toEqual([{ cmd: 'set_setting', key: MEMORY, value: 'new_worktree' }]);
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
      ['the user typed another path', async (daemon: ScriptedDaemon) => typePath(daemon, '/tmp/other')],
      ['the picker was closed and opened again', async (daemon: ScriptedDaemon) => {
        await gesture(daemon, () => fireEvent.click(screen.getByTestId('location-picker-overlay')));
        await gesture(daemon, () => pressShortcut('session.new'));
      }],
    ])('ignores a path inspection answered after %s, success or failure', async (_, moveOn) => {
      const { daemon } = await openPicker({ repos: [EXSIN] });
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
      await gesture(daemon, () => pressShortcut('session.new'));
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

});
