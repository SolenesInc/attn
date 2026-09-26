import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture, pressShortcut } from './test/renderApp';
import type { ScriptedDaemon } from './test/scriptedDaemon';
import { chosenRow, destinationMemory, HOME, launchedAt, openPicker, pathInput, press, repoInfo, submitPath } from './test/locations';

const REPO = `${HOME}/projects/repo`;
const FEATURE = `${REPO}--feature`;
const MEMORY = destinationMemory(REPO);
const WITH_FEATURE = repoInfo(REPO, [{ path: FEATURE, branch: 'feature' }], {
  branches: [{ name: 'main' }, { name: 'feature' }, { name: 'spike' }],
});

async function openChooser(path = REPO, settings: Record<string, string> = {}) {
  const view = await openPicker({ repos: [WITH_FEATURE] }, { settings });
  await submitPath(view.daemon, path);
  return view;
}

const chooser = () => screen.getByTestId('repo-options');
const createInput = () => screen.getByTestId('repo-new-worktree-input') as HTMLInputElement;
const rows = () => screen.queryAllByTestId(/^repo-option-\d+$/);
const deletePrompt = () => screen.queryByText(/Delete repo--feature/);

async function inChooser(daemon: ScriptedDaemon, key: string) {
  await press(daemon, key, {}, chooser());
}

describe('App new session in a repository', () => {
  it('lands on the worktree a typed path names, listing the checkout and its worktrees but not its branches', async () => {
    await openChooser(FEATURE);

    expect(rows()).toHaveLength(2);
    expect(chosenRow()).toBe(1);
    expect(screen.getByTestId('repo-option-1')).toHaveTextContent('feature');
  });

  it('opens a typed existing worktree on Enter without creating one, even when the repository was last run in its checkout', async () => {
    const { daemon } = await openChooser(FEATURE, { [MEMORY]: 'main_repo' });
    expect(chosenRow()).toBe(1);

    await inChooser(daemon, 'Enter');

    expect(daemon.sentOf('create_worktree')).toEqual([]);
    expect(launchedAt(daemon)).toEqual([{ cwd: FEATURE, agent: 'claude' }]);
  });

  it('offers a generated adjective-noun worktree name for the repository root, and draws another on request', async () => {
    const { daemon } = await openChooser();
    expect(createInput()).toHaveFocus();
    expect(createInput().value).toMatch(/^[a-z]+-[a-z]+$/);
    expect(chosenRow()).toBe(-1);
    const offered = createInput().value;

    await gesture(daemon, () => fireEvent.click(screen.getByRole('button', { name: 'Pick another name' })));

    expect(createInput().value).toMatch(/^[a-z]+-[a-z]+$/);
    expect(createInput().value).not.toBe(offered);
  });

  it('keeps the create form focused for a repository last used through a new worktree', async () => {
    await openChooser(REPO, { [MEMORY]: 'new_worktree' });

    expect(createInput()).toHaveFocus();
  });

  it('moves between the create form and the destinations with the arrows', async () => {
    const { daemon } = await openChooser(REPO, { [MEMORY]: 'main_repo' });
    expect(chosenRow()).toBe(0);

    await inChooser(daemon, 'ArrowUp');
    expect(createInput()).toHaveFocus();

    await inChooser(daemon, 'ArrowDown');
    expect(chosenRow()).toBe(0);
    await inChooser(daemon, 'Enter');
    expect(launchedAt(daemon)).toEqual([{ cwd: REPO, agent: 'claude' }]);
  });

  it('creates a worktree from origin/<default branch>, or from the chosen worktree’s branch, and starts the session in it', async () => {
    const { daemon } = await openChooser(FEATURE);
    const create = async (name: string, from?: 'current') => {
      await gesture(daemon, () => fireEvent.click(screen.getByTestId('repo-new-worktree-form')));
      fireEvent.change(createInput(), { target: { value: name } });
      if (from) fireEvent.click(screen.getByTestId('repo-new-worktree-start-current'));
      else expect(screen.getByTestId('repo-new-worktree-start-default')).toBeChecked();
      await inChooser(daemon, 'Enter');
    };

    await create('feature-2');
    await gesture(daemon, () => pressShortcut('session.newWorkspace'));
    await submitPath(daemon, FEATURE);
    await create('feature-3', 'current');

    expect(daemon.sentOf('create_worktree').map(({ branch, starting_from }) => ({ branch, starting_from }))).toEqual([
      { branch: 'feature-2', starting_from: 'origin/main' },
      { branch: 'feature-3', starting_from: 'feature' },
    ]);
    expect(launchedAt(daemon).map(({ cwd }) => cwd)).toEqual([`${REPO}--feature-2`, `${REPO}--feature-3`]);
  });

  it.each([
    ['a branch that already exists', "git worktree add failed: fatal: a branch named 'mine' already exists"],
    ['any other failure', 'git worktree add failed: fatal: not a git repository'],
  ])('reports %s for a typed worktree name once, without retrying or launching', async (_, error) => {
    const { daemon } = await openChooser();
    daemon.on('create_worktree', () => ({ event: 'create_worktree_result', success: false, error }));

    fireEvent.change(createInput(), { target: { value: 'mine' } });
    await inChooser(daemon, 'Enter');

    expect(daemon.sentOf('create_worktree').map(({ branch }) => branch)).toEqual(['mine']);
    expect(screen.getByRole('alert')).toHaveTextContent(error);
    expect(launchedAt(daemon)).toEqual([]);
  });

  it('leaves the letter d to the create form rather than arming a delete', async () => {
    const { daemon } = await openChooser(FEATURE);
    await inChooser(daemon, 'ArrowUp');
    await inChooser(daemon, 'ArrowUp');
    expect(createInput()).toHaveFocus();

    await inChooser(daemon, 'd');

    expect(deletePrompt()).toBeNull();
  });

  it('drops a delete prompt on Escape without leaving the chooser, and when the create form takes focus', async () => {
    const { daemon } = await openChooser(FEATURE);

    await inChooser(daemon, 'd');
    expect(deletePrompt()).not.toBeNull();
    await inChooser(daemon, 'Escape');
    expect(deletePrompt()).toBeNull();
    expect(chooser()).toBeInTheDocument();

    await inChooser(daemon, 'd');
    await gesture(daemon, () => fireEvent.click(screen.getByTestId('repo-new-worktree-form')));
    expect(deletePrompt()).toBeNull();
    expect(createInput()).toHaveFocus();
    expect(daemon.sentOf('delete_worktree')).toEqual([]);
  });

  it('goes back to the path from the create form on Escape', async () => {
    const { daemon } = await openChooser();

    await inChooser(daemon, 'Escape');

    expect(screen.queryByTestId('repo-options')).toBeNull();
    expect(pathInput()).toBeInTheDocument();
  });

  it('keeps the destinations listed while the repository is read again', async () => {
    const { daemon } = await openChooser(FEATURE);
    daemon.on('get_repo_info', () => undefined);

    await inChooser(daemon, 'r');

    expect(screen.getByRole('status', { name: 'Refreshing repo options' })).toBeInTheDocument();
    expect(rows()).toHaveLength(2);
  });

  it('shows which worktree is being deleted until the daemon answers', async () => {
    const { daemon } = await openChooser(FEATURE);
    daemon.on('delete_worktree', () => undefined);

    await inChooser(daemon, 'd');
    await inChooser(daemon, 'y');

    expect(screen.getByRole('status', { name: 'Deleting repo--feature' })).toBeInTheDocument();
    expect(deletePrompt()).toBeNull();
    expect(daemon.sentOf('delete_worktree')).toEqual([{ cmd: 'delete_worktree', path: FEATURE }]);
  });
});
