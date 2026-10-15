import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut } from './test/renderApp';
import {
  HOME,
  destinationMemory,
  inspection,
  launchedAt,
  openPicker,
  pathInput,
  press,
  repoInfo,
  typePath,
} from './test/locations';
import type { ScriptedDaemon } from './test/scriptedDaemon';

const REPO = `${HOME}/projects/shop`;
const TREE = `${REPO}--feature`;
const INFO = repoInfo(REPO, [{ path: TREE, branch: 'feature' }], { current_branch: 'topic' });
const settings = {
  new_session_launcher_enabled: 'true',
  new_session_agent: 'claude',
  projects_directory: `${HOME}/projects`,
};

async function open(memory = 'main_repo') {
  const view = await openPicker(
    {
      recent: [REPO],
      repos: [INFO],
      directories: { [`${HOME}/projects`]: ['shop', 'never-opened'] },
    },
    { settings: { ...settings, [destinationMemory(REPO)]: memory } },
  );
  return view;
}
function memoryWrites(daemon: ScriptedDaemon) {
  return daemon
    .sentOf('set_setting')
    .filter((command) => command.key.startsWith('new_session_destination'));
}
async function where(daemon: ScriptedDaemon) {
  await press(daemon, 'Tab');
  expect(screen.getByRole('listbox', { name: 'Where' })).toBeInTheDocument();
  expect(daemon.sentOf('get_repo_info').map(({ repo }) => repo)).toEqual([REPO]);
}

describe('one-screen launcher', () => {
  it.each(['main_repo', 'new_worktree'])(
    'starts the top recent in its remembered %s destination',
    async (memory) => {
      const { daemon } = await open(memory);
      expect(pathInput()).toHaveValue('');
      await press(daemon, 'Enter');
      const creates = daemon.sentOf('create_worktree');
      expect(creates).toHaveLength(memory === 'new_worktree' ? 1 : 0);
      if (memory === 'new_worktree')
        expect(creates[0]).toMatchObject({
          main_repo: REPO,
          starting_from: 'origin/main',
          branch: expect.stringMatching(/\w+-\w+/),
        });
      expect(launchedAt(daemon)).toEqual([
        {
          cwd: memory === 'new_worktree' ? `${REPO}--${creates[0].branch}` : REPO,
          agent: 'claude',
        },
      ]);
      expect(memoryWrites(daemon)).toEqual([]);
    },
  );

  it('searches recents and folders of the projects directory once per opening', async () => {
    const { daemon } = await open();
    const before = daemon.sentOf('browse_directory').length;
    await typePath(daemon, 'SHOp');
    expect(screen.getAllByRole('option')).toHaveLength(1);
    await typePath(daemon, 'never');
    expect(screen.getByRole('option')).toHaveTextContent('never-opened/');
    expect(daemon.sentOf('browse_directory')).toHaveLength(before);
    await press(daemon, 'Enter');
    expect(launchedAt(daemon)).toEqual([{ cwd: `${HOME}/projects/never-opened`, agent: 'claude' }]);
    expect(daemon.sentOf('inspect_path')).toEqual([]);
  });

  it.each([REPO, `${REPO}/`])('opens where for a typed checkout %s', async (path) => {
    const { daemon } = await open();
    await typePath(daemon, path);
    await where(daemon);
    expect(pathInput()).toHaveValue('');
    await press(daemon, 'Backspace');
    expect(pathInput()).toHaveValue(path);
    await press(daemon, 'Escape');
    expect(pathInput()).toHaveValue('');
    expect(screen.getByTestId('location-picker')).toBeInTheDocument();
  });

  it.each([
    ['default', 'origin/main'],
    ['current', 'topic'],
  ])('creates a named worktree from %s', async (from, ref) => {
    const { daemon } = await open();
    await where(daemon);
    await typePath(daemon, 'fix-login');
    if (from === 'current') await press(daemon, 'ArrowDown');
    await press(daemon, 'Enter');
    expect(
      daemon
        .sentOf('create_worktree')
        .map(({ main_repo, branch, starting_from }) => ({ main_repo, branch, starting_from })),
    ).toEqual([{ main_repo: REPO, branch: 'fix-login', starting_from: ref }]);
    expect(launchedAt(daemon)).toEqual([{ cwd: `${REPO}--fix-login`, agent: 'claude' }]);
    expect(memoryWrites(daemon).map(({ key, value }) => ({ key, value }))).toEqual([
      { key: destinationMemory(REPO), value: 'new_worktree' },
    ]);
  });

  it('starts an existing worktree without writing the destination memory', async () => {
    const { daemon } = await open('new_worktree');
    await where(daemon);
    await typePath(daemon, 'feature');
    await press(daemon, 'Enter');
    expect(launchedAt(daemon)).toEqual([{ cwd: TREE, agent: 'claude' }]);
    expect(daemon.sentOf('create_worktree')).toEqual([]);
    expect(memoryWrites(daemon)).toEqual([]);
  });

  it('opens a terminal in the main checkout with CmdEnter, regardless of the remembered destination', async () => {
    const { daemon } = await open('new_worktree');
    await press(daemon, 'Enter', { metaKey: true });
    expect(launchedAt(daemon)).toEqual([{ cwd: REPO, agent: 'shell' }]);
    expect(daemon.sentOf('create_worktree')).toEqual([]);
    expect(memoryWrites(daemon)).toEqual([]);
  });

  it('creates a new worktree for CmdEnter and opens a terminal without memory writes', async () => {
    const { daemon } = await open();
    await where(daemon);
    await typePath(daemon, 'terminal-work');
    await press(daemon, 'Enter', { metaKey: true });
    expect(
      daemon
        .sentOf('create_worktree')
        .map(({ branch, starting_from }) => ({ branch, starting_from })),
    ).toEqual([{ branch: 'terminal-work', starting_from: 'origin/main' }]);
    expect(launchedAt(daemon)).toEqual([{ cwd: `${REPO}--terminal-work`, agent: 'shell' }]);
    expect(memoryWrites(daemon)).toEqual([]);
  });

  it.each(['true', 'false'])(
    'CmdT selects Terminal for one opening with the experiment %s',
    async (enabled) => {
      const { daemon } = await openPicker(
        {},
        { settings: { new_session_launcher_enabled: enabled, new_session_agent: 'claude' } },
      );
      await press(daemon, 'Escape');
      await gesture(daemon, () => pressShortcut('session.newTerminal'));
      expect(screen.getByRole('radio', { name: /Terminal/ })).toHaveAttribute(
        'aria-checked',
        'true',
      );
      await press(daemon, 'Escape');
      await gesture(daemon, () => pressShortcut('session.new'));
      expect(screen.getByRole('radio', { name: /Claude/ })).toHaveAttribute('aria-checked', 'true');
      expect(daemon.sentOf('set_setting')).toEqual([]);
    },
  );

  it('leaves the form and inline error in place when a named branch is refused', async () => {
    const { daemon } = await open();
    await where(daemon);
    await typePath(daemon, 'duplicate');
    daemon.on('create_worktree', () => ({
      event: 'create_worktree_result',
      success: false,
      error: "a branch named 'duplicate' already exists",
    }));
    await press(daemon, 'Enter');
    expect(pathInput()).toHaveValue('duplicate');
    expect(screen.getByRole('alert')).toHaveTextContent('already exists');
    expect(screen.getByRole('option', { selected: true })).toHaveTextContent('from main');
    expect(launchedAt(daemon)).toEqual([]);
    expect(memoryWrites(daemon)).toEqual([]);
  });

  it('keeps a created worktree after startup refusal and retries startup without creating it again', async () => {
    const { daemon } = await open();
    await where(daemon);
    await typePath(daemon, 'keep-me');
    daemon.on('spawn_session', ({ id }) => ({
      event: 'spawn_result',
      id,
      success: false,
      error: 'provider refused',
    }));
    await press(daemon, 'Enter');
    expect(pathInput()).toHaveValue('keep-me');
    expect(screen.getByRole('alert')).toHaveTextContent('provider refused');
    await press(daemon, 'Enter');
    expect(daemon.sentOf('create_worktree')).toHaveLength(1);
    expect(daemon.sentOf('spawn_session')).toHaveLength(2);
  });

  it('confirms deletion, handles the forceable refusal, and keeps the row index', async () => {
    const { daemon } = await open();
    await where(daemon);
    await typePath(daemon, 'feature');
    daemon.on('delete_worktree', ({ force, path }) =>
      force
        ? { event: 'delete_worktree_result', success: true, path }
        : {
            event: 'delete_worktree_result',
            success: false,
            forceable: true,
            error: 'dirty worktree',
            path,
          },
    );
    await press(daemon, 'Backspace', { metaKey: true });
    expect(screen.getByRole('alert')).toHaveTextContent('Delete feature?');
    await press(daemon, 'y');
    expect(screen.getByText('Force delete local worktree and branch? (y/n)')).toBeInTheDocument();
    await press(daemon, 'y');
    expect(daemon.sentOf('delete_worktree').map(({ path, force }) => ({ path, force }))).toEqual([
      { path: TREE, force: undefined },
      { path: TREE, force: true },
    ]);
    expect(screen.getAllByRole('option')).toHaveLength(3);
    expect(screen.getByRole('option', { selected: true })).toHaveTextContent('main checkout');
  });

  it('keeps a failed repository lookup in the where list until Escape', async () => {
    const { daemon } = await open();
    daemon.on('get_repo_info', () => ({
      event: 'get_repo_info_result',
      success: false,
      info: { ...INFO },
      error: 'repository is unavailable',
    }));
    await press(daemon, 'Tab');
    expect(screen.getByRole('alert')).toHaveTextContent('repository is unavailable');
    expect(pathInput()).toHaveValue('');
    await press(daemon, 'Escape');
    expect(screen.getByRole('listbox', { name: 'Locations' })).toBeInTheDocument();
  });

  it('does not highlight stale recents before their reply or start on bare Enter', async () => {
    const { daemon } = await open();
    await press(daemon, 'Escape');
    daemon.on('get_recent_locations', () => undefined);
    await gesture(daemon, () => pressShortcut('session.new'));
    expect(screen.queryByRole('option', { selected: true })).toBeNull();
    await press(daemon, 'Enter');
    expect(launchedAt(daemon)).toEqual([]);
  });

  it('reports a missing typed directory inline without launching', async () => {
    const { daemon } = await open();
    daemon.on('inspect_path', ({ path }) => inspection(path, { exists: false }));
    await typePath(daemon, '/missing');
    await press(daemon, 'Enter');
    expect(screen.getByRole('alert')).toHaveTextContent('Directory not found: /missing');
    expect(pathInput()).toHaveValue('/missing');
    expect(launchedAt(daemon)).toEqual([]);
  });
});
